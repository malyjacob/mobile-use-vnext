package protocol

import (
	"encoding/json/v2"
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
			body, err := (&ActRequest{ObservationID: "O1", Operation: *operation, Image: true}).Encode()
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
		body, err := (&InspectRequest{ObservationID: "O2", Query: "确定", HasQuery: true}).Encode()
		if err != nil {
			t.Fatal(err)
		}
		req, apiErr := DecodeInspect(body)
		if apiErr != nil || req.Query != "确定" || !req.HasQuery || req.HasNode {
			t.Fatalf("DecodeInspect: %v %+v", apiErr, req)
		}
		if _, err := (&InspectRequest{ObservationID: "O2", Node: "n1", HasNode: true, Query: "x", HasQuery: true}).Encode(); err == nil {
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
		apps := &AppsRequest{Query: "设置", HasQuery: true}
		body, err = apps.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeApps(body); apiErr != nil || req.Query != "设置" || !req.HasQuery {
			t.Fatalf("DecodeApps: %v %+v", apiErr, req)
		}
		openApp := &OpenAppRequest{ObservationID: "O4", App: "com.android.settings"}
		body, err = openApp.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if req, apiErr := DecodeOpenApp(body); apiErr != nil || req.App != "com.android.settings" {
			t.Fatalf("DecodeOpenApp: %v %+v", apiErr, req)
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
	body, err := (&ActRequest{ObservationID: "O1", Operation: Operation{Type: "swipe", From: &Point{}, To: &Point{X: 1, Y: 2}}}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for name := range raw {
		switch name {
		case "observation_id", "operation", "image":
		default:
			t.Fatalf("unexpected member %q", name)
		}
	}
	if strings.Contains(string(body), "null") {
		t.Fatalf("encoder wrote null: %s", body)
	}
}
