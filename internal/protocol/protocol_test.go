package protocol

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"
)

func TestStrictAdmission(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"start empty", `{}`, true},
		{"start unknown", `{"mode":"virtual"}`, false},
		{"duplicate member", `{"reference":"O1","reference":"O2"}`, false},
		{"case variant", `{"Reference":"O1"}`, false},
		{"trailing value", `{"reference":"O1"} {"reference":"O2"}`, false},
		{"status null ref", `{"reference":null}`, false},
		{"status number ref", `{"reference":1}`, false},
		{"observe ok", `{"reference":"O1","full":true}`, true},
		{"observe null full", `{"reference":"O1","full":null}`, false},
		{"observe missing ref", `{}`, false},
		{"observe wait negative", `{"reference":"O1","wait_ms":-1}`, false},
		{"observe wait over", `{"reference":"O1","wait_ms":10001}`, false},
		{"end ok", `{"reference":"P1"}`, true},
		{"end unknown", `{"reference":"P1","session_id":"x"}`, false},
		{"act ok tap ref", `{"observation_id":"O1","operation":{"type":"tap","target":{"ref":"n1"}}}`, true},
		{"act tap coords", `{"observation_id":"O1","operation":{"type":"tap","target":{"x":1,"y":2}}}`, true},
		{"act tap bool coord", `{"observation_id":"O1","operation":{"type":"tap","target":{"x":true,"y":2}}}`, false},
		{"act tap float coord", `{"observation_id":"O1","operation":{"type":"tap","target":{"x":1.5,"y":2}}}`, false},
		{"act tap both", `{"observation_id":"O1","operation":{"type":"tap","target":{"ref":"n1","x":1,"y":2}}}`, false},
		{"act tap one coord", `{"observation_id":"O1","operation":{"type":"tap","target":{"x":1}}}`, false},
		{"act swipe ok", `{"observation_id":"O1","operation":{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4}}}`, true},
		{"act swipe same", `{"observation_id":"O1","operation":{"type":"swipe","from":{"x":1,"y":2},"to":{"x":1,"y":2}}}`, false},
		{"act swipe extra", `{"observation_id":"O1","operation":{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"text":"x"}}`, false},
		{"act key ok", `{"observation_id":"O1","operation":{"type":"key","key":"BACK"}}`, true},
		{"act key bad", `{"observation_id":"O1","operation":{"type":"key","key":"VOLUME_UP"}}`, false},
		{"act key null", `{"observation_id":"O1","operation":{"type":"key","key":null}}`, false},
		{"act key extra", `{"observation_id":"O1","operation":{"type":"key","key":"BACK","target":{"ref":"n1"}}}`, false},
		{"act text empty", `{"observation_id":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":""}}`, true},
		{"act text null", `{"observation_id":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":null}}`, false},
		{"act text missing", `{"observation_id":"O1","operation":{"type":"text","target":{"ref":"n1"}}}`, false},
		{"act unknown op", `{"observation_id":"O1","operation":{"type":"hover"}}`, false},
		{"act unknown member", `{"observation_id":"O1","operation":{"type":"tap","target":{"ref":"n1"},"duration":10}}`, false},
		{"act image null", `{"observation_id":"O1","operation":{"type":"tap","target":{"ref":"n1"}},"image":null}`, false},
		{"wait_for ok sleep", `{"reference":"O1","timeout":1}`, true},
		{"wait_for ok conditions", `{"reference":"O1","query":"加载","absent_query":"错误","timeout":40,"stable":true,"full":true,"image":true}`, true},
		{"wait_for missing timeout", `{"reference":"O1"}`, false},
		{"wait_for null timeout", `{"reference":"O1","timeout":null}`, false},
		{"wait_for timeout zero", `{"reference":"O1","timeout":0}`, false},
		{"wait_for timeout over", `{"reference":"O1","timeout":41}`, false},
		{"wait_for timeout float", `{"reference":"O1","timeout":1.5}`, false},
		{"wait_for missing reference", `{"timeout":5}`, false},
		{"wait_for null reference", `{"reference":null,"timeout":5}`, false},
		{"wait_for stable short", `{"reference":"O1","timeout":1,"stable":true}`, false},
		{"wait_for null query", `{"reference":"O1","timeout":5,"query":null}`, false},
		{"wait_for blank query", `{"reference":"O1","timeout":5,"query":"   "}`, false},
		{"wait_for blank absent", `{"reference":"O1","timeout":5,"absent_query":""}`, false},
		{"wait_for same condition", `{"reference":"O1","timeout":5,"query":"加载","absent_query":"加载"}`, false},
		{"wait_for null stable", `{"reference":"O1","timeout":5,"stable":null}`, false},
		{"wait_for unknown member", `{"reference":"O1","timeout":5,"wait_ms":10}`, false},
		{"wait_for duplicate member", `{"reference":"O1","timeout":5,"timeout":6}`, false},
		{"wait_for case variant", `{"Reference":"O1","timeout":5}`, false},
		{"wait_for trailing value", `{"reference":"O1","timeout":5} {}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err *APIError
			switch {
			case strings.HasPrefix(tc.name, "start"):
				_, err = DecodeStart([]byte(tc.body))
			case strings.HasPrefix(tc.name, "status"):
				_, err = DecodeStatus([]byte(tc.body))
			case strings.HasPrefix(tc.name, "observe"):
				_, err = DecodeObserve([]byte(tc.body))
			case strings.HasPrefix(tc.name, "end"):
				_, err = DecodeEnd([]byte(tc.body))
			case strings.HasPrefix(tc.name, "wait_for"):
				_, err = DecodeWaitFor([]byte(tc.body))
			default:
				_, err = DecodeAct([]byte(tc.body))
			}
			if tc.ok && err != nil {
				t.Fatalf("expected admission, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected rejection")
			}
		})
	}
}

func TestTextTooLong(t *testing.T) {
	text := strings.Repeat("测", MaxTextRunes+1)
	body := `{"observation_id":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"` + text + `"}}`
	if _, err := DecodeAct([]byte(body)); err == nil {
		t.Fatal("expected text_too_long")
	}
}

// actBody wraps one raw operation object in the act envelope.
func actBody(operation string) string {
	return `{"observation_id":"O1","operation":` + operation + `}`
}

// TestDecodeActScrollSchema is the strict schema matrix for the scroll branch:
// valid container selectors, all four directions, the page/half amount and the
// page default; every unknown, missing, null or foreign member is rejected.
func TestDecodeActScrollSchema(t *testing.T) {
	cases := []struct {
		name          string
		operation     string
		ok            bool
		wantContainer *Target
		wantDirection string
		wantAmount    string
	}{
		{
			name:          "ref container page",
			operation:     `{"type":"scroll","container":{"ref":"n372"},"direction":"down","amount":"page"}`,
			ok:            true,
			wantContainer: &Target{UseRef: true, Ref: "n372"},
			wantDirection: ScrollDirectionDown,
			wantAmount:    ScrollAmountPage,
		},
		{
			name:          "point container default amount",
			operation:     `{"type":"scroll","container":{"x":540,"y":1200},"direction":"up"}`,
			ok:            true,
			wantContainer: &Target{X: 540, Y: 1200},
			wantDirection: ScrollDirectionUp,
			wantAmount:    ScrollAmountPage,
		},
		{
			name:          "direction left half",
			operation:     `{"type":"scroll","container":{"ref":"n1"},"direction":"left","amount":"half"}`,
			ok:            true,
			wantContainer: &Target{UseRef: true, Ref: "n1"},
			wantDirection: ScrollDirectionLeft,
			wantAmount:    ScrollAmountHalf,
		},
		{
			name:          "direction right half",
			operation:     `{"type":"scroll","container":{"x":0,"y":0},"direction":"right","amount":"half"}`,
			ok:            true,
			wantContainer: &Target{X: 0, Y: 0},
			wantDirection: ScrollDirectionRight,
			wantAmount:    ScrollAmountHalf,
		},
		{
			name:          "upper case direction",
			operation:     `{"type":"scroll","container":{"ref":"n1"},"direction":"DOWN"}`,
			ok:            false,
			wantDirection: ScrollDirectionDown,
		},
		{
			name:      "unknown direction",
			operation: `{"type":"scroll","container":{"ref":"n1"},"direction":"forward"}`,
			ok:        false,
		},
		{
			name:      "unknown amount",
			operation: `{"type":"scroll","container":{"ref":"n1"},"direction":"down","amount":"full"}`,
			ok:        false,
		},
		{
			name:      "missing container",
			operation: `{"type":"scroll","direction":"down"}`,
			ok:        false,
		},
		{
			name:      "missing direction",
			operation: `{"type":"scroll","container":{"ref":"n1"}}`,
			ok:        false,
		},
		{
			name:      "null container",
			operation: `{"type":"scroll","container":null,"direction":"down"}`,
			ok:        false,
		},
		{
			name:      "null direction",
			operation: `{"type":"scroll","container":{"ref":"n1"},"direction":null}`,
			ok:        false,
		},
		{
			name:      "null amount",
			operation: `{"type":"scroll","container":{"ref":"n1"},"direction":"down","amount":null}`,
			ok:        false,
		},
		{
			name:      "numeric direction",
			operation: `{"type":"scroll","container":{"ref":"n1"},"direction":1}`,
			ok:        false,
		},
		{
			name:      "numeric amount",
			operation: `{"type":"scroll","container":{"ref":"n1"},"direction":"down","amount":1}`,
			ok:        false,
		},
		{
			name:      "container ref and point",
			operation: `{"type":"scroll","container":{"ref":"n1","x":1,"y":2},"direction":"down"}`,
			ok:        false,
		},
		{
			name:      "container one coordinate",
			operation: `{"type":"scroll","container":{"x":1},"direction":"down"}`,
			ok:        false,
		},
		{
			name:      "container empty ref",
			operation: `{"type":"scroll","container":{"ref":""},"direction":"down"}`,
			ok:        false,
		},
		{
			name:      "container negative point",
			operation: `{"type":"scroll","container":{"x":-1,"y":2},"direction":"down"}`,
			ok:        false,
		},
		{
			name:      "container unknown member",
			operation: `{"type":"scroll","container":{"ref":"n1","role":"scroll"},"direction":"down"}`,
			ok:        false,
		},
		{
			name:      "duplicate direction",
			operation: `{"type":"scroll","container":{"ref":"n1"},"direction":"down","direction":"up"}`,
			ok:        false,
		},
		{
			name:      "duplicate container",
			operation: `{"type":"scroll","container":{"ref":"n1"},"container":{"ref":"n2"},"direction":"down"}`,
			ok:        false,
		},
		{
			name:      "duplicate container ref",
			operation: `{"type":"scroll","container":{"ref":"n1","ref":"n2"},"direction":"down"}`,
			ok:        false,
		},
		{
			name:      "unknown operation member",
			operation: `{"type":"scroll","container":{"ref":"n1"},"direction":"down","scrollable":true}`,
			ok:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := DecodeAct([]byte(actBody(tc.operation)))
			if !tc.ok {
				if err == nil {
					t.Fatalf("expected rejection for %s", tc.operation)
				}
				if err.Code != CodeInvalidRequest {
					t.Fatalf("expected invalid_request, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected admission for %s: %v", tc.operation, err)
			}
			if req.Operation.Type != "scroll" {
				t.Fatalf("type = %q", req.Operation.Type)
			}
			if !reflect.DeepEqual(req.Operation.Container, tc.wantContainer) {
				t.Fatalf("container = %#v, want %#v", req.Operation.Container, tc.wantContainer)
			}
			if req.Operation.Direction != tc.wantDirection {
				t.Fatalf("direction = %q, want %q", req.Operation.Direction, tc.wantDirection)
			}
			if req.Operation.Amount != tc.wantAmount {
				t.Fatalf("amount = %q, want %q", req.Operation.Amount, tc.wantAmount)
			}
		})
	}
}

// TestDecodeActSwipeDuration is the strict duration matrix: omitted means the
// fixed default, the inclusive bounds are admitted, and null/zero/negative/
// out-of-range/fractional/string values are rejected.
func TestDecodeActSwipeDuration(t *testing.T) {
	swipe := func(extra string) string {
		return `{"type":"swipe","from":{"x":500,"y":1800},"to":{"x":500,"y":600}` + extra + `}`
	}
	cases := []struct {
		name      string
		operation string
		ok        bool
		want      int
	}{
		{"omitted default", swipe(""), true, DefaultSwipeDurationMS},
		{"minimum", swipe(`,"duration_ms":50`), true, MinSwipeDurationMS},
		{"default explicit", swipe(`,"duration_ms":300`), true, DefaultSwipeDurationMS},
		{"maximum", swipe(`,"duration_ms":2000`), true, MaxSwipeDurationMS},
		{"zero", swipe(`,"duration_ms":0`), false, 0},
		{"negative", swipe(`,"duration_ms":-1`), false, 0},
		{"below minimum", swipe(`,"duration_ms":49`), false, 0},
		{"above maximum", swipe(`,"duration_ms":2001`), false, 0},
		{"fractional", swipe(`,"duration_ms":300.5`), false, 0},
		{"string", swipe(`,"duration_ms":"300"`), false, 0},
		{"null", swipe(`,"duration_ms":null`), false, 0},
		{"bool", swipe(`,"duration_ms":true`), false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := DecodeAct([]byte(actBody(tc.operation)))
			if !tc.ok {
				if err == nil {
					t.Fatalf("expected rejection for %s", tc.operation)
				}
				if err.Code != CodeInvalidRequest {
					t.Fatalf("expected invalid_request, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected admission for %s: %v", tc.operation, err)
			}
			if req.Operation.DurationMS != tc.want {
				t.Fatalf("duration_ms = %d, want %d", req.Operation.DurationMS, tc.want)
			}
		})
	}
}

// TestDecodeActBranchMutex is the reverse control for the strict branch mutex.
// Every case is a member that belongs to another branch; removing any of the
// per-branch rejection checks makes the corresponding case decode successfully
// and this test fails.
func TestDecodeActBranchMutex(t *testing.T) {
	cases := []struct {
		name      string
		operation string
	}{
		{"scroll with target", `{"type":"scroll","container":{"ref":"n1"},"direction":"down","target":{"ref":"n2"}}`},
		{"scroll with null target", `{"type":"scroll","container":{"ref":"n1"},"direction":"down","target":null}`},
		{"scroll with from", `{"type":"scroll","container":{"ref":"n1"},"direction":"down","from":{"x":1,"y":2}}`},
		{"scroll with to", `{"type":"scroll","container":{"ref":"n1"},"direction":"down","to":{"x":1,"y":2}}`},
		{"scroll with key", `{"type":"scroll","container":{"ref":"n1"},"direction":"down","key":"BACK"}`},
		{"scroll with text", `{"type":"scroll","container":{"ref":"n1"},"direction":"down","text":"x"}`},
		{"scroll with duration", `{"type":"scroll","container":{"ref":"n1"},"direction":"down","duration_ms":300}`},
		{"swipe with container", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"container":{"ref":"n1"}}`},
		{"swipe with null container", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"container":null}`},
		{"swipe with direction", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"direction":"down"}`},
		{"swipe with amount", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"amount":"page"}`},
		{"swipe with target", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"target":{"ref":"n1"}}`},
		{"swipe with null target", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"target":null}`},
		{"swipe with key", `{"type":"swipe","from":{"x":1,"y":2},"to":{"x":3,"y":4},"key":"BACK"}`},
		{"swipe with null from", `{"type":"swipe","from":null,"to":{"x":3,"y":4}}`},
		{"tap with container", `{"type":"tap","target":{"ref":"n1"},"container":{"ref":"n2"}}`},
		{"tap with null from", `{"type":"tap","target":{"ref":"n1"},"from":null}`},
		{"tap with direction", `{"type":"tap","target":{"ref":"n1"},"direction":"down"}`},
		{"tap with amount", `{"type":"tap","target":{"ref":"n1"},"amount":"page"}`},
		{"tap with duration", `{"type":"tap","target":{"ref":"n1"},"duration_ms":300}`},
		{"long press with duration", `{"type":"long_press","target":{"ref":"n1"},"duration_ms":300}`},
		{"text with duration", `{"type":"text","target":{"ref":"n1"},"text":"x","duration_ms":300}`},
		{"text with direction", `{"type":"text","target":{"ref":"n1"},"text":"x","direction":"down"}`},
		{"key with container", `{"type":"key","key":"BACK","container":{"ref":"n1"}}`},
		{"key with direction", `{"type":"key","key":"BACK","direction":"down"}`},
		{"key with amount", `{"type":"key","key":"BACK","amount":"page"}`},
		{"key with duration", `{"type":"key","key":"BACK","duration_ms":300}`},
		{"key with null target", `{"type":"key","key":"BACK","target":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeAct([]byte(actBody(tc.operation))); err == nil {
				t.Fatalf("branch mutex admitted %s", tc.operation)
			}
		})
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestNodeDefaultEncoding(t *testing.T) {
	yes := true
	node := &Node{
		Ref: "n1", Role: RoleButton, Bounds: &Bounds{Left: 1, Top: 2, Right: 3, Bottom: 4},
		Enabled: &yes, Label: []string{"hi"}, Actions: []Action{ActionTap},
	}
	got := string(marshal(t, node))
	if strings.Contains(got, `"enabled"`) {
		t.Fatalf("known enabled=true must be omitted: %s", got)
	}
	if !strings.Contains(got, `"bounds":[1,2,3,4]`) {
		t.Fatalf("bounds missing: %s", got)
	}

	unknown := &Node{Ref: "n2", Role: RoleText, Bounds: nil, Enabled: nil, Checked: nil, Value: strptr("")}
	got = string(marshal(t, unknown))
	if !strings.Contains(got, `"enabled":null`) {
		t.Fatalf("unknown enabled must be explicit null: %s", got)
	}
	if !strings.Contains(got, `"checked":null`) {
		t.Fatalf("unknown checked must be explicit null: %s", got)
	}
	if !strings.Contains(got, `"value":""`) {
		t.Fatalf("empty known value must be explicit: %s", got)
	}
	if !strings.Contains(got, `"bounds":null`) {
		t.Fatalf("missing bounds must be null: %s", got)
	}
}

func strptr(s string) *string { return &s }

func optionalBoolPtr(v bool) *bool { return &v }

func sameOptionalBool(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func optionalBoolString(value *bool) string {
	if value == nil {
		return "null"
	}
	if *value {
		return "true"
	}
	return "false"
}

func TestNodeRoundTripStates(t *testing.T) {
	f := false
	empty := ""
	original := &Node{
		Ref: "n1", Role: RoleInput, Bounds: &Bounds{0, 0, 10, 10},
		Value: &empty, Enabled: nil, Focused: &f, Protected: nil,
		Checked: nil, Children: []*Node{{Ref: "n2", Role: RoleText, Bounds: &Bounds{1, 1, 2, 2}}},
	}
	data := marshal(t, original)
	var decoded Node
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal node: %v", err)
	}
	if decoded.Enabled != nil {
		t.Fatal("enabled null should decode as unknown")
	}
	if decoded.Focused == nil || *decoded.Focused != false {
		t.Fatal("absent focused should decode as known false")
	}
	if decoded.Protected != nil {
		t.Fatal("protected null should decode as unknown")
	}
	if decoded.Checked != nil {
		t.Fatal("checked null should decode as unknown")
	}
	if decoded.Value == nil || *decoded.Value != "" {
		t.Fatal("empty value lost")
	}
	if len(decoded.Children) != 1 {
		t.Fatal("child lost")
	}
	again := marshal(t, &decoded)
	if string(again) != string(data) {
		t.Fatalf("round trip changed encoding:\n%s\n%s", data, again)
	}
}

func TestNodeTriStateWireMatrix(t *testing.T) {
	fields := []struct {
		name         string
		defaultValue bool
		set          func(*Node, *bool)
		get          func(*Node) *bool
	}{
		{"enabled", true, func(n *Node, value *bool) { n.Enabled = value }, func(n *Node) *bool { return n.Enabled }},
		{"focused", false, func(n *Node, value *bool) { n.Focused = value }, func(n *Node) *bool { return n.Focused }},
		{"selected", false, func(n *Node, value *bool) { n.Selected = value }, func(n *Node) *bool { return n.Selected }},
		{"protected", false, func(n *Node, value *bool) { n.Protected = value }, func(n *Node) *bool { return n.Protected }},
		{"checked", false, func(n *Node, value *bool) { n.Checked = value }, func(n *Node) *bool { return n.Checked }},
	}
	for _, field := range fields {
		for _, state := range []string{"known default", "non-default known", "unknown"} {
			t.Run(field.name+"/"+state, func(t *testing.T) {
				var value *bool
				switch state {
				case "known default":
					value = optionalBoolPtr(field.defaultValue)
				case "non-default known":
					value = optionalBoolPtr(!field.defaultValue)
				}
				node := &Node{Ref: "n1", Role: RoleButton}
				field.set(node, value)
				wire := string(marshal(t, node))
				member := `"` + field.name + `":`

				switch state {
				case "known default":
					if strings.Contains(wire, member) {
						t.Fatalf("known default must be omitted: %s", wire)
					}
				case "non-default known":
					want := "true"
					if field.defaultValue {
						want = "false"
					}
					if !strings.Contains(wire, member+want) {
						t.Fatalf("non-default value missing: %s", wire)
					}
				case "unknown":
					if !strings.Contains(wire, member+"null") {
						t.Fatalf("unknown value must be explicit null: %s", wire)
					}
				}

				var decoded Node
				if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got := field.get(&decoded); !sameOptionalBool(got, value) {
					t.Fatalf("decoded %s = %v, want %v", field.name, optionalBoolString(got), optionalBoolString(value))
				}
			})
		}
	}
}

func TestReadNodeTriStateWireMatrix(t *testing.T) {
	fields := []struct {
		name         string
		defaultValue bool
		set          func(*ReadNode, *bool)
		get          func(*ReadNode) *bool
	}{
		{"enabled", true, func(n *ReadNode, value *bool) { n.Enabled = value }, func(n *ReadNode) *bool { return n.Enabled }},
		{"focused", false, func(n *ReadNode, value *bool) { n.Focused = value }, func(n *ReadNode) *bool { return n.Focused }},
		{"selected", false, func(n *ReadNode, value *bool) { n.Selected = value }, func(n *ReadNode) *bool { return n.Selected }},
		{"protected", false, func(n *ReadNode, value *bool) { n.Protected = value }, func(n *ReadNode) *bool { return n.Protected }},
		{"checked", false, func(n *ReadNode, value *bool) { n.Checked = value }, func(n *ReadNode) *bool { return n.Checked }},
	}
	for _, field := range fields {
		for _, state := range []string{"known default", "non-default known", "unknown"} {
			t.Run(field.name+"/"+state, func(t *testing.T) {
				var value *bool
				switch state {
				case "known default":
					value = optionalBoolPtr(field.defaultValue)
				case "non-default known":
					value = optionalBoolPtr(!field.defaultValue)
				}
				node := &ReadNode{Ref: "n1", Role: RoleButton, Bounds: HiddenBounds()}
				field.set(node, value)
				wire := string(marshal(t, node))
				member := `"` + field.name + `":`

				switch state {
				case "known default":
					if strings.Contains(wire, member) {
						t.Fatalf("known default must be omitted: %s", wire)
					}
				case "non-default known":
					want := "true"
					if field.defaultValue {
						want = "false"
					}
					if !strings.Contains(wire, member+want) {
						t.Fatalf("non-default value missing: %s", wire)
					}
				case "unknown":
					if !strings.Contains(wire, member+"null") {
						t.Fatalf("unknown value must be explicit null: %s", wire)
					}
				}

				var decoded ReadNode
				if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got := field.get(&decoded); !sameOptionalBool(got, value) {
					t.Fatalf("decoded %s = %v, want %v", field.name, optionalBoolString(got), optionalBoolString(value))
				}
			})
		}
	}
}

func TestSetNodeExplicitStateValues(t *testing.T) {
	zeroBounds := Bounds{}
	emptyValue := ""
	set := SetNode{
		Bounds:  Field[*Bounds]{Set: true, Value: &zeroBounds},
		Label:   Field[[]string]{Set: true, Value: []string{}},
		Actions: Field[[]Action]{Set: true, Value: []Action{}},
		Enabled: Field[*bool]{Set: true, Value: optionalBoolPtr(false)},
		Focused: Field[*bool]{Set: true, Value: optionalBoolPtr(true)},
		Checked: Field[*bool]{Set: true, Value: nil},
		Value:   Field[*string]{Set: true, Value: &emptyValue},
	}
	wire := string(marshal(t, &Delta{Format: "delta", Base: "O1", Set: map[string]SetNode{"n2": set}}))
	for _, want := range []string{
		`"bounds":[0,0,0,0]`, `"label":[]`, `"actions":[]`,
		`"enabled":false`, `"focused":true`, `"checked":null`, `"value":""`,
	} {
		if !strings.Contains(wire, want) {
			t.Fatalf("set wire missing %s: %s", want, wire)
		}
	}

	var decoded Delta
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatalf("decode set: %v", err)
	}
	got := decoded.Set["n2"]
	if !got.Bounds.Set || got.Bounds.Value == nil || *got.Bounds.Value != zeroBounds {
		t.Fatalf("zero bounds lost: %#v", got.Bounds)
	}
	if !got.Label.Set || got.Label.Value == nil || len(got.Label.Value) != 0 {
		t.Fatalf("empty label lost: %#v", got.Label)
	}
	if !got.Actions.Set || got.Actions.Value == nil || len(got.Actions.Value) != 0 {
		t.Fatalf("empty actions lost: %#v", got.Actions)
	}
	if !got.Enabled.Set || got.Enabled.Value == nil || *got.Enabled.Value {
		t.Fatalf("enabled=false lost: %#v", got.Enabled)
	}
	if !got.Focused.Set || got.Focused.Value == nil || !*got.Focused.Value {
		t.Fatalf("focused=true lost: %#v", got.Focused)
	}
	if !got.Checked.Set || got.Checked.Value != nil {
		t.Fatalf("checked=null lost: %#v", got.Checked)
	}
	if !got.Value.Set || got.Value.Value == nil || *got.Value.Value != "" {
		t.Fatalf("empty value lost: %#v", got.Value)
	}
}

func TestReadSetNodeExplicitStateValues(t *testing.T) {
	zeroBounds := KnownBounds(Bounds{})
	emptyValue := ""
	set := ReadSetNode{
		Bounds:  Field[ReadBounds]{Set: true, Value: zeroBounds},
		Label:   Field[[]string]{Set: true, Value: []string{}},
		Actions: Field[[]Action]{Set: true, Value: []Action{}},
		Enabled: Field[*bool]{Set: true, Value: optionalBoolPtr(false)},
		Focused: Field[*bool]{Set: true, Value: optionalBoolPtr(true)},
		Checked: Field[*bool]{Set: true, Value: nil},
		Value:   Field[*string]{Set: true, Value: &emptyValue},
	}
	wire := string(marshal(t, &ReadDelta{
		Format: "delta", Base: "O1", View: ViewCompact,
		Set: map[string]ReadSetNode{"n2": set},
	}))
	for _, want := range []string{
		`"bounds":[0,0,0,0]`, `"label":[]`, `"actions":[]`,
		`"enabled":false`, `"focused":true`, `"checked":null`, `"value":""`,
	} {
		if !strings.Contains(wire, want) {
			t.Fatalf("read set wire missing %s: %s", want, wire)
		}
	}
	if strings.Contains(wire, `"defaults"`) {
		t.Fatalf("read delta carried defaults: %s", wire)
	}

	var decoded ReadDelta
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatalf("decode read set: %v", err)
	}
	got := decoded.Set["n2"]
	if !got.Bounds.Set || !got.Bounds.Value.Equal(zeroBounds) {
		t.Fatalf("zero bounds lost: %#v", got.Bounds)
	}
	if !got.Label.Set || got.Label.Value == nil || len(got.Label.Value) != 0 {
		t.Fatalf("empty label lost: %#v", got.Label)
	}
	if !got.Actions.Set || got.Actions.Value == nil || len(got.Actions.Value) != 0 {
		t.Fatalf("empty actions lost: %#v", got.Actions)
	}
	if !got.Enabled.Set || got.Enabled.Value == nil || *got.Enabled.Value {
		t.Fatalf("enabled=false lost: %#v", got.Enabled)
	}
	if !got.Focused.Set || got.Focused.Value == nil || !*got.Focused.Value {
		t.Fatalf("focused=true lost: %#v", got.Focused)
	}
	if !got.Checked.Set || got.Checked.Value != nil {
		t.Fatalf("checked=null lost: %#v", got.Checked)
	}
	if !got.Value.Set || got.Value.Value == nil || *got.Value.Value != "" {
		t.Fatalf("empty value lost: %#v", got.Value)
	}
}

func TestReadTreeAndDeltaWireWithoutLegacyDeclaration(t *testing.T) {
	tree := &ReadTree{
		Format: "full", Scope: Scope, View: ViewCompact,
		Root: &ReadNode{
			Ref: "n1", Role: RoleRoot, Bounds: KnownBounds(Bounds{0, 0, 1, 1}),
			Checked: optionalBoolPtr(false),
		},
	}
	wire := string(marshal(t, tree))
	if strings.Contains(wire, `"defaults"`) {
		t.Fatalf("read tree carried defaults: %s", wire)
	}
	if strings.Contains(wire, `"checked"`) {
		t.Fatalf("known-default checked was not omitted: %s", wire)
	}
	var decodedTree ReadTree
	if err := json.Unmarshal([]byte(wire), &decodedTree); err != nil {
		t.Fatalf("decode read tree: %v", err)
	}
	if decodedTree.Root == nil || decodedTree.Root.Checked == nil || *decodedTree.Root.Checked {
		t.Fatalf("absent checked did not restore known false: %#v", decodedTree.Root)
	}

	delta := &ReadDelta{Format: "delta", Base: "O1", View: ViewCompact}
	if got := string(marshal(t, delta)); got != `{"format":"delta","base":"O1","view":"compact"}` {
		t.Fatalf("empty delta wire: %s", got)
	}

	for _, tc := range []struct {
		name string
		body string
		dst  any
	}{
		{
			name: "read tree",
			body: `{"format":"full","scope":"reported_visible_with_context","view":"compact",` +
				`"defaults":{"checked":false},"root":{"ref":"n1","role":"root","bounds":[0,0,1,1]}}`,
			dst: &ReadTree{},
		},
		{
			name: "read delta",
			body: `{"format":"delta","base":"O1","view":"compact","defaults":{"checked":false}}`,
			dst:  &ReadDelta{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tc.body), tc.dst); err == nil {
				t.Fatalf("accepted removed defaults: %s", tc.body)
			}
		})
	}
}

func TestTreeAndDeltaEncoding(t *testing.T) {
	yes, no := true, false
	full := &Tree{Root: &Node{Ref: "n1", Role: RoleRoot, Bounds: &Bounds{0, 0, 1080, 2400},
		Enabled: &yes, Focused: &no, Selected: &no, Protected: &no}}
	got := string(marshal(t, full))
	want := `{"format":"full","scope":"reported_visible_with_context","root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400],"checked":null}}`
	if got != want {
		t.Fatalf("full encoding:\n got %s\nwant %s", got, want)
	}

	emptyDelta := &Delta{Format: "delta", Base: "O100"}
	if got := string(marshal(t, emptyDelta)); got != `{"format":"delta","base":"O100"}` {
		t.Fatalf("empty delta: %s", got)
	}

	label := []string{}
	value := ""
	delta := &Delta{
		Format: "delta", Base: "O100",
		Set: map[string]SetNode{
			"n2": {Checked: Field[*bool]{Set: true, Value: &yes}},
			"n3": {Value: Field[*string]{Set: true, Value: &value},
				Label:   Field[[]string]{Set: true, Value: label},
				Enabled: Field[*bool]{Set: true, Value: nil},
				Focused: Field[*bool]{Set: true, Value: &no}},
		},
	}
	got = string(marshal(t, delta))
	for _, want := range []string{`"checked":true`, `"value":""`, `"label":[]`, `"enabled":null`, `"focused":false`} {
		if !strings.Contains(got, want) {
			t.Fatalf("delta missing %s: %s", want, got)
		}
	}

	var decoded Delta
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("delta decode: %v", err)
	}
	if !decoded.Set["n3"].Value.Set || *decoded.Set["n3"].Value.Value != "" {
		t.Fatal("delta value round trip lost")
	}
	if !decoded.Set["n3"].Enabled.Set || decoded.Set["n3"].Enabled.Value != nil {
		t.Fatal("delta enabled null lost")
	}
}

func TestTreeValueDecode(t *testing.T) {
	var full TreeValue
	if err := json.Unmarshal([]byte(`{"format":"full","scope":"reported_visible_with_context","root":{"ref":"n1","role":"root","bounds":null}}`), &full); err != nil {
		t.Fatalf("full tree value: %v", err)
	}
	if full.Full == nil || full.Full.Root == nil {
		t.Fatal("full tree missing")
	}
	var delta TreeValue
	if err := json.Unmarshal([]byte(`{"format":"delta","base":"O1","set":{"n2":{"checked":false}}}`), &delta); err != nil {
		t.Fatalf("delta tree value: %v", err)
	}
	if delta.Delta == nil || delta.Delta.Set["n2"].Checked.Value == nil {
		t.Fatal("delta missing")
	}
}

func TestObservationEncoding(t *testing.T) {
	full := &ReadTree{
		Format: "full", Scope: Scope, View: ViewCompact,
		Root: &ReadNode{Ref: "n1", Role: RoleRoot, Bounds: KnownBounds(Bounds{0, 0, 10, 10})},
	}
	obs := &Observation{
		Status: "complete", ID: "O1", Origin: "P1", Revision: 1,
		CapturedAt: "2026-11-08T00:00:00Z", ActionableUntil: "2026-11-08T00:01:00Z",
		Screen:     &Screen{Width: 1080, Height: 2400, Rotation: 0},
		Foreground: &Foreground{State: "present", Package: "example.app", TaskID: intptr(42), HasTask: true},
		Tree:       &ReadTreeValue{Full: full},
	}
	got := string(marshal(t, obs))
	for _, want := range []string{`"id":"O1"`, `"origin":"P1"`, `"revision":1`, `"task_id":42`, `"format":"full"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("observation missing %s: %s", want, got)
		}
	}
}

func intptr(v int) *int { return &v }

// TestWaitForEncodeRoundTrip pins the canonical wire form of one condition wait:
// an omitted condition is an absent member, a long Unicode timeout is rejected
// past the documented bound, and the result fact keeps its member order and its
// never-null matched list.
func TestWaitForEncodeRoundTrip(t *testing.T) {
	request := &WaitForRequest{
		Reference: "O1", Query: "加载", HasQuery: true, Timeout: 15,
	}
	body, err := request.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := `{"reference":"O1","query":"加载","timeout":15,"stable":false,"full":false,"image":false}`
	if string(body) != want {
		t.Fatalf("encode = %s, want %s", body, want)
	}
	decoded, apiErr := DecodeWaitFor(body)
	if apiErr != nil {
		t.Fatalf("decode: %v", apiErr)
	}
	if *decoded != *request {
		t.Fatalf("round trip = %+v, want %+v", decoded, request)
	}

	long := strings.Repeat("测", MaxQueryRunes+1)
	if _, apiErr := DecodeWaitFor([]byte(`{"reference":"O1","timeout":5,"query":"` + long + `"}`)); apiErr == nil {
		t.Fatal("an over-long condition was admitted")
	}

	for _, raw := range []string{
		`{"wait":{"trigger":"query","matched":["query","timeout"],"elapsed_ms":1200}}`,
		`{"wait":{"trigger":"stable","elapsed_ms":0}}`,
	} {
		var result Result
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		if result.Wait == nil || result.Wait.Trigger == "" {
			t.Fatalf("decoded wait is empty: %+v", result.Wait)
		}
	}
	for _, raw := range []string{
		`{"wait":{"trigger":"poll","elapsed_ms":1}}`,
		`{"wait":{"trigger":"query","matched":null,"elapsed_ms":1}}`,
		`{"wait":{"trigger":"query","matched":[],"elapsed_ms":1}}`,
		`{"wait":{"trigger":"query","matched":["stable"],"elapsed_ms":1}}`,
		`{"wait":{"trigger":"query","elapsed_ms":-1}}`,
		`{"wait":{"trigger":"query"}}`,
	} {
		var result Result
		if err := json.Unmarshal([]byte(raw), &result); err == nil {
			t.Fatalf("admitted invalid wait result %s", raw)
		}
	}
}

func TestEmptyDeltaDecode(t *testing.T) {
	var d Delta
	if err := json.Unmarshal([]byte(`{"format":"delta","base":"O100"}`), &d); err != nil {
		t.Fatalf("empty delta decode: %v", err)
	}
	if !d.IsEmpty() {
		t.Fatal("expected empty delta")
	}
}

// ensure the encoder is used with raw tokens, not a second JSON dialect.
var _ = jsontext.Null
var _ = bytes.MinRead
