package protocol

import (
	"strings"
	"testing"
)

func TestControlSyncWireCarriesMessagesAndAcks(t *testing.T) {
	view := ControlView{Revision: 3, Availability: Availability{State: "ready"}}
	response := &AppSyncResponse{
		ContractVersion:   ContractVersion,
		ServiceInstanceID: "d0e00000-0000-4000-8000-000000000000",
		Control:           view,
		Commands:          []AppCommand{},
		Messages: []AppMessage{
			{ID: "M7", Seq: 7, Text: "消息正文", CreatedAt: "2026-11-09T04:00:00Z"},
		},
	}
	data, err := JSON(response)
	if err != nil {
		t.Fatalf("encode sync response: %v", err)
	}
	encoded := string(data)
	for _, want := range []string{
		`"contract_version":"` + ContractVersion + `"`,
		`"messages":[{"id":"M7","seq":7,"text":"消息正文","created_at":"2026-11-09T04:00:00Z"}]`,
		`"commands":[]`,
	} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("sync response missing %s: %s", want, encoded)
		}
	}

	request := `{"contract_version":"` + ContractVersion + `","app_instance_id":"11111111-1111-4111-8111-111111111111",` +
		`"app_version":"0.4.0","selected_mode":"virtual","mode_generation":"22222222-2222-4222-8222-222222222222",` +
		`"mode_ready":true,"stop_ready":true,"notifications_enabled":true,"background_running":true,` +
		`"power_interactive":true,"keyguard_locked":false,` +
		`"primary_display":{"display_id":0,"width":1080,"height":2400,"density_dpi":420,"rotation":0,"generation":"22222222-2222-4222-8222-222222222222"},` +
		`"display":null,"command_results":[],"stop_pending":null,"message_acks":["M1","M2"]}`
	var decoded AppSyncRequest
	if err := Decode([]byte(request), &decoded); err != nil {
		t.Fatalf("decode sync request: %v", err)
	}
	if len(decoded.MessageAcks) != 2 || decoded.MessageAcks[0] != "M1" {
		t.Fatalf("message_acks not decoded: %+v", decoded.MessageAcks)
	}

	// An old App remains parseable at the wire level; the contract check, not
	// field absence, decides compatibility.
	old := strings.Replace(request, `"message_acks":["M1","M2"]`, `"message_acks":[]`, 1)
	if err := Decode([]byte(old), &decoded); err != nil {
		t.Fatalf("decode empty message_acks: %v", err)
	}

	unknown := strings.Replace(request, `"message_acks":["M1","M2"]`,
		`"message_acks":[],"inbox":[]`, 1)
	if err := Decode([]byte(unknown), &decoded); err == nil {
		t.Fatal("unknown sync member was accepted")
	}
}
