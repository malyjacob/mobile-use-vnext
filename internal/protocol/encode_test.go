package protocol

import (
	"encoding/json/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Every request encoder must produce a body that the matching strict decoder
// admits. This keeps the client and MCP canonicalization from drifting away
// from the HTTP admission shape.
func TestRequestEncodersRoundTrip(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		body, err := (&StartRequest{}).Encode()
		if err != nil || string(body) != "{}" {
			t.Fatalf("Encode=%s err=%v", body, err)
		}
		if _, apiErr := DecodeStart(body); apiErr != nil {
			t.Fatalf("DecodeStart: %v", apiErr)
		}
	})
	t.Run("status", func(t *testing.T) {
		body, err := (&StatusRequest{}).Encode()
		if err != nil || string(body) != "{}" {
			t.Fatalf("Encode=%s err=%v", body, err)
		}
		req, apiErr := DecodeStatus(body)
		if apiErr != nil || req.Reference != "" {
			t.Fatalf("DecodeStatus: %v %+v", apiErr, req)
		}
		body, err = (&StatusRequest{Reference: "P3", HasRef: true}).Encode()
		if err != nil {
			t.Fatal(err)
		}
		req, apiErr = DecodeStatus(body)
		if apiErr != nil || req.Reference != "P3" || !req.HasRef {
			t.Fatalf("DecodeStatus: %v %+v", apiErr, req)
		}
	})
	t.Run("observe", func(t *testing.T) {
		body, err := (&ObserveRequest{Reference: "O1", Full: true, Image: true, WaitMS: 25}).Encode()
		if err != nil {
			t.Fatal(err)
		}
		req, apiErr := DecodeObserve(body)
		if apiErr != nil || req.Reference != "O1" || !req.Full || !req.Image || req.WaitMS != 25 {
			t.Fatalf("DecodeObserve: %v %+v", apiErr, req)
		}
		if !strings.Contains(string(body), `"image":true`) {
			t.Fatalf("image not explicit: %s", body)
		}
	})
	t.Run("act branches", func(t *testing.T) {
		branches := []*Operation{
			{Type: "tap", Target: &Target{UseRef: true, Ref: "n8"}},
			{Type: "long_press", Target: &Target{X: 120, Y: 300}},
			{Type: "text", Target: &Target{UseRef: true, Ref: "n9"}, Text: ""},
			{Type: "swipe", From: &Point{X: 500, Y: 1800}, To: &Point{X: 500, Y: 600}},
			{Type: "key", Key: "BACK"},
		}
		for _, operation := range branches {
			body, err := (&ActRequest{Reference: "O1", Operation: *operation, Image: true}).Encode()
			if err != nil {
				t.Fatalf("%s: %v", operation.Type, err)
			}
			req, apiErr := DecodeAct(body)
			if apiErr != nil {
				t.Fatalf("%s: %v (%s)", operation.Type, apiErr, body)
			}
			if req.Operation.Type != operation.Type {
				t.Fatalf("%s: type=%s", operation.Type, req.Operation.Type)
			}
			if operation.Type == "text" && req.Operation.Text != "" {
				t.Fatalf("empty text was not preserved: %+v", req.Operation)
			}
		}
	})
	t.Run("inspect", func(t *testing.T) {
		body, err := (&InspectRequest{Reference: "O2", Query: "确定", HasQuery: true}).Encode()
		if err != nil {
			t.Fatal(err)
		}
		req, apiErr := DecodeInspect(body)
		if apiErr != nil || req.Query != "确定" || !req.HasQuery || req.HasNode {
			t.Fatalf("DecodeInspect: %v %+v", apiErr, req)
		}
		if _, err := (&InspectRequest{Reference: "O2", Node: "n1", HasNode: true, Query: "x", HasQuery: true}).Encode(); err == nil {
			t.Fatal("mutually exclusive inspect members were encoded")
		}
	})
	t.Run("others", func(t *testing.T) {
		keepalive := &KeepaliveRequest{Reference: "O1"}
		body, err := keepalive.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeKeepalive(body); apiErr != nil || req.Reference != "O1" {
			t.Fatalf("DecodeKeepalive: %v %+v", apiErr, req)
		}
		end := &EndRequest{Reference: "P1"}
		body, err = end.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeEnd(body); apiErr != nil || req.Reference != "P1" {
			t.Fatalf("DecodeEnd: %v %+v", apiErr, req)
		}
		find := &AppRequest{Kind: AppKindFind, Query: "设置", HasQuery: true}
		body, err = find.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeApp(body); apiErr != nil || req.Query != "设置" ||
			!req.HasQuery || req.Kind != AppKindFind {
			t.Fatalf("DecodeApp find: %v %+v", apiErr, req)
		}
		openApp := &AppRequest{Kind: AppKindOpen, Reference: "O4",
			Target: &AppTarget{App: "com.android.settings"}}
		body, err = openApp.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeApp(body); apiErr != nil || req.Target == nil ||
			req.Target.App != "com.android.settings" || req.Kind != AppKindOpen {
			t.Fatalf("DecodeApp open: %v %+v", apiErr, req)
		}
		kill := &AppRequest{Kind: AppKindKill, Reference: "O4",
			Target: &AppTarget{TaskID: 42, UseTask: true}, AllowStopPlayback: true}
		body, err = kill.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeApp(body); apiErr != nil || req.Target == nil ||
			req.Target.TaskID != 42 || !req.AllowStopPlayback {
			t.Fatalf("DecodeApp kill: %v %+v", apiErr, req)
		}
		notify := &NotifyRequest{Text: "消息正文"}
		body, err = notify.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeNotify(body); apiErr != nil || req.Text != "消息正文" {
			t.Fatalf("DecodeNotify: %v %+v", apiErr, req)
		}
		shell := &ShellRequest{Reference: "O1", Command: "id -u", Root: true}
		body, err = shell.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeShell(body); apiErr != nil || req.Command != "id -u" || !req.Root {
			t.Fatalf("DecodeShell: %v %+v", apiErr, req)
		}
		diagnose := &DiagnoseRequest{Reference: "O2", HasRef: true}
		body, err = diagnose.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeDiagnose(body); apiErr != nil || req.Reference != "O2" || !req.HasRef {
			t.Fatalf("DecodeDiagnose: %v %+v", apiErr, req)
		}
	})
}

func TestKeepaliveDurationEncoding(t *testing.T) {
	for _, req := range []KeepaliveRequest{
		{Reference: "O1"},
		{Reference: "O1", DurationMinutes: 121}, // No presence: this value is not a supplied parameter.
		{Reference: "O1", DurationMinutes: 1, HasDurationMinutes: true},
		{Reference: "O1", DurationMinutes: 2, HasDurationMinutes: true},
		{Reference: "O1", DurationMinutes: 120, HasDurationMinutes: true},
	} {
		body, err := req.Encode()
		if err != nil {
			t.Fatalf("Encode(%+v): %v", req, err)
		}
		if !req.HasDurationMinutes && string(body) != `{"reference":"O1"}` {
			t.Fatalf("omitted duration was encoded: %s", body)
		}
		decoded, apiErr := DecodeKeepalive(body)
		if apiErr != nil {
			t.Fatalf("DecodeKeepalive(%s): %v", body, apiErr)
		}
		if decoded.HasDurationMinutes != req.HasDurationMinutes ||
			(req.HasDurationMinutes && decoded.DurationMinutes != req.DurationMinutes) {
			t.Fatalf("duration presence/value lost: request=%+v decoded=%+v", req, decoded)
		}
	}
	for _, minutes := range []int{-1, 0, 121} {
		req := &KeepaliveRequest{Reference: "O1", DurationMinutes: minutes, HasDurationMinutes: true}
		if _, apiErr := req.EffectiveDurationMinutes(); apiErr == nil || apiErr.Code != CodeInvalidRequest {
			t.Fatalf("explicit invalid duration %d passed direct validation: %v", minutes, apiErr)
		}
		if body, err := req.Encode(); err == nil || body != nil {
			t.Fatalf("explicit invalid duration %d encoded: %s err=%v", minutes, body, err)
		}
	}
}

// Encoded members stay within the documented public vocabulary: no Go field
// names, no aliases and no explicit nulls.
func TestEncodedMembersArePublic(t *testing.T) {
	body, err := (&ActRequest{Reference: "O1", Operation: Operation{Type: "swipe", From: &Point{}, To: &Point{X: 1, Y: 2}}}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for name := range raw {
		switch name {
		case "reference", "operation", "image":
		default:
			t.Fatalf("unexpected member %q", name)
		}
	}
	if strings.Contains(string(body), "null") {
		t.Fatalf("encoder wrote null: %s", body)
	}
}

// TestActScrollEncodeRoundTrip proves the scroll encoder writes exactly the
// strict container/direction/amount shape the decoder admits.
func TestActScrollEncodeRoundTrip(t *testing.T) {
	cases := []struct {
		name        string
		operation   Operation
		omitAmount  bool
		wantAmount  string
		wantMembers []string
	}{
		{
			name:        "ref container page",
			operation:   Operation{Type: "scroll", Container: &Target{UseRef: true, Ref: "n372"}, Direction: ScrollDirectionDown, Amount: ScrollAmountPage},
			wantAmount:  ScrollAmountPage,
			wantMembers: []string{`"container":{"ref":"n372"}`, `"direction":"down"`, `"amount":"page"`},
		},
		{
			name:        "point container half",
			operation:   Operation{Type: "scroll", Container: &Target{X: 540, Y: 1200}, Direction: ScrollDirectionLeft, Amount: ScrollAmountHalf},
			wantAmount:  ScrollAmountHalf,
			wantMembers: []string{`"container":{"x":540,"y":1200}`, `"direction":"left"`, `"amount":"half"`},
		},
		{
			name:        "omitted amount decodes to page",
			operation:   Operation{Type: "scroll", Container: &Target{UseRef: true, Ref: "n1"}, Direction: ScrollDirectionUp},
			omitAmount:  true,
			wantAmount:  ScrollAmountPage,
			wantMembers: []string{`"direction":"up"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := (&ActRequest{Reference: "O1", Operation: tc.operation}).Encode()
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			for _, member := range tc.wantMembers {
				if !strings.Contains(string(body), member) {
					t.Fatalf("body missing %s: %s", member, body)
				}
			}
			if tc.omitAmount && strings.Contains(string(body), `"amount"`) {
				t.Fatalf("omitted amount was encoded: %s", body)
			}
			req, apiErr := DecodeAct(body)
			if apiErr != nil {
				t.Fatalf("DecodeAct(%s): %v", body, apiErr)
			}
			decoded := req.Operation
			if decoded.Type != "scroll" {
				t.Fatalf("type = %q", decoded.Type)
			}
			if !reflect.DeepEqual(decoded.Container, tc.operation.Container) {
				t.Fatalf("container = %#v, want %#v", decoded.Container, tc.operation.Container)
			}
			if decoded.Direction != tc.operation.Direction {
				t.Fatalf("direction = %q", decoded.Direction)
			}
			if decoded.Amount != tc.wantAmount {
				t.Fatalf("amount = %q, want %q", decoded.Amount, tc.wantAmount)
			}
			if decoded.DurationMS != 0 || decoded.Target != nil || decoded.From != nil ||
				decoded.To != nil || decoded.Key != "" || decoded.Text != "" {
				t.Fatalf("scroll carried a foreign member: %#v", decoded)
			}
		})
	}
}

// TestSwipeDurationEncode proves duration_ms is emitted only for a positive,
// in-range swipe duration and that the decoder restores the same value.
func TestSwipeDurationEncode(t *testing.T) {
	base := func(duration int) Operation {
		return Operation{Type: "swipe", From: &Point{X: 500, Y: 1800},
			To: &Point{X: 500, Y: 600}, DurationMS: duration}
	}
	for _, duration := range []int{MinSwipeDurationMS, DefaultSwipeDurationMS, MaxSwipeDurationMS} {
		body, err := (&ActRequest{Reference: "O1", Operation: base(duration)}).Encode()
		if err != nil {
			t.Fatalf("duration %d: %v", duration, err)
		}
		want := `"duration_ms":` + strconv.Itoa(duration)
		if !strings.Contains(string(body), want) {
			t.Fatalf("duration %d not encoded: %s", duration, body)
		}
		req, apiErr := DecodeAct(body)
		if apiErr != nil {
			t.Fatalf("DecodeAct(%s): %v", body, apiErr)
		}
		if req.Operation.DurationMS != duration {
			t.Fatalf("duration round trip = %d, want %d", req.Operation.DurationMS, duration)
		}
	}

	body, err := (&ActRequest{Reference: "O1", Operation: base(0)}).Encode()
	if err != nil {
		t.Fatalf("omitted duration: %v", err)
	}
	if strings.Contains(string(body), "duration_ms") {
		t.Fatalf("zero duration was encoded: %s", body)
	}
	req, apiErr := DecodeAct(body)
	if apiErr != nil {
		t.Fatalf("DecodeAct(%s): %v", body, apiErr)
	}
	if req.Operation.DurationMS != DefaultSwipeDurationMS {
		t.Fatalf("omitted duration = %d, want %d", req.Operation.DurationMS, DefaultSwipeDurationMS)
	}
}

// TestActEncoderRejectsInvalidShapes is the encoder-side reverse control: a
// caller cannot canonicalize a shape the strict decoder would reject. Removing
// any branch guard makes the corresponding case encode and this test fail.
func TestActEncoderRejectsInvalidShapes(t *testing.T) {
	point := &Target{X: 1, Y: 2}
	operations := []Operation{
		{Type: "scroll"},
		{Type: "scroll", Container: &Target{UseRef: true}, Direction: ScrollDirectionDown},
		{Type: "scroll", Container: point, Direction: "DOWN"},
		{Type: "scroll", Container: point},
		{Type: "scroll", Container: point, Direction: ScrollDirectionDown, Amount: "full"},
		{Type: "scroll", Container: point, Direction: ScrollDirectionDown, DurationMS: 300},
		{Type: "scroll", Container: point, Direction: ScrollDirectionDown, From: &Point{}},
		{Type: "scroll", Container: point, Direction: ScrollDirectionDown, Target: &Target{UseRef: true, Ref: "n1"}},
		{Type: "scroll", Container: point, Direction: ScrollDirectionDown, Key: "BACK"},
		{Type: "scroll", Container: point, Direction: ScrollDirectionDown, Text: "x"},
		{Type: "scroll", Container: &Target{X: -1, Y: 2}, Direction: ScrollDirectionDown},
		{Type: "swipe", From: &Point{}, To: &Point{X: 1, Y: 2}, Container: &Target{UseRef: true, Ref: "n1"}},
		{Type: "swipe", From: &Point{}, To: &Point{X: 1, Y: 2}, Direction: ScrollDirectionDown},
		{Type: "swipe", From: &Point{}, To: &Point{X: 1, Y: 2}, Amount: ScrollAmountPage},
		{Type: "swipe", From: &Point{X: 1, Y: 2}, To: &Point{X: 1, Y: 2}},
		{Type: "swipe", From: &Point{}, To: &Point{X: 1, Y: 2}, DurationMS: 49},
		{Type: "swipe", From: &Point{}, To: &Point{X: 1, Y: 2}, DurationMS: 2001},
		{Type: "swipe", From: &Point{}, To: &Point{X: 1, Y: 2}, DurationMS: -1},
		{Type: "tap", Target: &Target{UseRef: true, Ref: "n1"}, DurationMS: 300},
		{Type: "long_press", Target: &Target{UseRef: true, Ref: "n1"}, DurationMS: 300},
		{Type: "text", Target: &Target{UseRef: true, Ref: "n1"}, Text: "x", DurationMS: 300},
		{Type: "key", Key: "BACK", Direction: ScrollDirectionDown},
		{Type: "key", Key: "BACK", Container: &Target{UseRef: true, Ref: "n1"}},
		{Type: "key", Key: "BACK", Amount: ScrollAmountHalf},
	}
	for _, operation := range operations {
		if body, err := (&ActRequest{Reference: "O1", Operation: operation}).Encode(); err == nil {
			t.Fatalf("encoder admitted invalid shape %#v: %s", operation, body)
		} else if body != nil {
			t.Fatalf("encoder returned a body on error: %s", body)
		}
	}
}
