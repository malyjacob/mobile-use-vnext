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
	tr := func(v bool) *bool { return &v }
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

	unknown := &Node{Ref: "n2", Role: RoleText, Bounds: nil, Enabled: nil, Checked: tr(false), Value: strptr("")}
	got = string(marshal(t, unknown))
	if !strings.Contains(got, `"enabled":null`) {
		t.Fatalf("unknown enabled must be explicit null: %s", got)
	}
	if !strings.Contains(got, `"checked":false`) {
		t.Fatalf("checked=false must be explicit: %s", got)
	}
	if !strings.Contains(got, `"value":""`) {
		t.Fatalf("empty known value must be explicit: %s", got)
	}
	if !strings.Contains(got, `"bounds":null`) {
		t.Fatalf("missing bounds must be null: %s", got)
	}
}

func strptr(s string) *string { return &s }

func TestNodeRoundTripDefaults(t *testing.T) {
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

func TestTreeAndDeltaEncoding(t *testing.T) {
	yes, no := true, false
	full := &Tree{Root: &Node{Ref: "n1", Role: RoleRoot, Bounds: &Bounds{0, 0, 1080, 2400},
		Enabled: &yes, Focused: &no, Selected: &no, Protected: &no}}
	got := string(marshal(t, full))
	want := `{"format":"full","scope":"reported_visible_with_context","root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}`
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
