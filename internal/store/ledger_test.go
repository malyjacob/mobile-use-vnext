package store

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/engine"
)

const testLedgerBootID = "123e4567-e89b-42d3-a456-426614174000"

func newTestLedgerStore(t *testing.T) *Ledger {
	t.Helper()
	ledger, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ledger
}

func validTestLedger(version int) *engine.Ledger {
	updatedAt := time.Date(2026, 11, 10, 3, 4, 5, 0, time.UTC)
	session := &engine.LedgerSession{
		Origin: "Pstart", Agent: "agent", Mode: "virtual", State: "active",
		AppInstanceID:  "11111111-1111-4111-8111-111111111111",
		ModeGeneration: "mode-generation",
		DisplayID:      12, SFID: 99, DisplayGeneration: "display-generation",
		Width: 1080, Height: 2400, Density: 420, Rotation: 0, StartedAt: updatedAt,
	}
	if version >= 3 {
		session.CreateCommand = "55555555-5555-4555-8555-555555555555"
		session.DeviceID = "device-private-a"
		session.BootID = testLedgerBootID
	}
	return &engine.Ledger{
		Format:            "mobile-use-vnext-ledger",
		Version:           version,
		Revision:          7,
		ServiceInstanceID: "service-old",
		UpdatedAt:         updatedAt,
		AppInstanceID:     "11111111-1111-4111-8111-111111111111",
		Session:           session,
		PendingCreate: &engine.LedgerCommand{
			ID: "22222222-2222-4222-8222-222222222222", Kind: "create_display",
			AppInstanceID: "11111111-1111-4111-8111-111111111111",
			RequestedAt:   updatedAt,
		},
		PendingClose: &engine.LedgerCommand{
			ID: "33333333-3333-4333-8333-333333333333", Kind: "close_display",
			AppInstanceID: "11111111-1111-4111-8111-111111111111",
			DisplayID:     12, DisplayGeneration: "display-generation", RequestedAt: updatedAt,
		},
		Cleanup: &engine.LedgerCleanup{
			State: "unconfirmed", Execution: "unconfirmed", Tasks: "confirmed",
			Recents: "unconfirmed", Display: "confirmed", Reason: "execution_unconfirmed",
			ExecReason: "ui_input", StartedAt: updatedAt,
		},
		Tasks: []engine.LedgerTask{{
			TaskID: 41, Package: "com.example.app", DisplayID: 12, Proven: true,
		}},
		Stop: &engine.LedgerStop{
			AppInstanceID: "11111111-1111-4111-8111-111111111111",
			Seq:           5, AcceptedAt: updatedAt,
		},
		Shell: &engine.LedgerShell{
			RunnerID: "44444444-4444-4444-8444-444444444444", OperationID: "Pshell",
			Origin: "Pstart", Epoch: 1,
			AppInstanceID:     "11111111-1111-4111-8111-111111111111",
			ModeGeneration:    "mode-generation",
			DisplayID:         12,
			DisplayGeneration: "display-generation",
			State:             "running", Phase: "running", CommandRunes: 12,
			StartedAt: updatedAt, DeadlineAt: updatedAt.Add(time.Minute),
			StartPID: 100, StartPGID: 100, StartTicks: 9, BootID: testLedgerBootID,
		},
	}
}

func validTestLedgerUI() *engine.LedgerUI {
	return &engine.LedgerUI{
		OperationID: "Pinput", Origin: "Pstart", Epoch: 1, Step: 2,
		Kind: "tap", DisplayID: 12, DisplayGeneration: "display-generation",
		DeviceID: "device-private-a", BootID: testLedgerBootID, State: "may_dispatch",
	}
}

func validTestLedgerCheckpoint() *engine.LedgerUICheckpoint {
	return &engine.LedgerUICheckpoint{
		Origin: "Plegacy", DeviceID: "device-private-a", BootID: testLedgerBootID,
	}
}

func writeLedgerRecord(t *testing.T, ledger *Ledger, record *engine.Ledger) []byte {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal ledger: %v", err)
	}
	writeLedgerBytes(t, ledger, data)
	return data
}

func writeLedgerBytes(t *testing.T, ledger *Ledger, data []byte) {
	t.Helper()
	if err := os.WriteFile(ledger.path, data, 0o600); err != nil {
		t.Fatalf("write ledger: %v", err)
	}
}

func TestLedgerLoadsV2UIEvidence(t *testing.T) {
	ledger := newTestLedgerStore(t)
	want := validTestLedger(2)
	want.UI = validTestLedgerUI()
	want.LegacyUI = validTestLedgerCheckpoint()
	want.Cleanup.CheckedTaskIDs = []int{41, 57}
	writeLedgerRecord(t, ledger, want)

	got, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestLedgerAcceptsAllowedUIKindsAndStates(t *testing.T) {
	kinds := []string{"tap", "long_press", "swipe", "key", "launch", "focus", "text"}
	states := []string{"may_dispatch", "terminated", "not_dispatched"}
	for _, kind := range kinds {
		for _, state := range states {
			t.Run(kind+"/"+state, func(t *testing.T) {
				ledger := newTestLedgerStore(t)
				record := validTestLedger(2)
				record.UI = validTestLedgerUI()
				record.UI.OperationID = "P" + strings.Repeat("a", 63)
				record.UI.Origin = "P" + strings.Repeat("b", 63)
				record.UI.DeviceID = strings.Repeat("d", 128)
				record.UI.Kind = kind
				record.UI.State = state
				if _, err := ledger.Save(context.Background(), record); err != nil {
					t.Fatalf("Save: %v", err)
				}
				got, err := ledger.Load()
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				if !reflect.DeepEqual(got.UI, record.UI) {
					t.Fatalf("ui = %+v, want %+v", got.UI, record.UI)
				}
			})
		}
	}
}

func TestLedgerSaveWritesV4WithoutMutatingCaller(t *testing.T) {
	ledger := newTestLedgerStore(t)
	record := validTestLedger(1)
	record.Format = "caller-format"
	record.Revision = 42
	record.UI = validTestLedgerUI()
	record.UI.State = "terminated"
	record.LegacyUI = validTestLedgerCheckpoint()
	record.Cleanup.CheckedTaskIDs = []int{41, 57}
	uiBefore := *record.UI
	checkpointBefore := *record.LegacyUI
	checkedBefore := append([]int(nil), record.Cleanup.CheckedTaskIDs...)

	if _, err := ledger.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if record.Format != "caller-format" || record.Version != 1 || record.Revision != 42 {
		t.Fatalf("Save mutated caller header: %+v", record)
	}
	if !reflect.DeepEqual(*record.UI, uiBefore) || !reflect.DeepEqual(*record.LegacyUI, checkpointBefore) ||
		!reflect.DeepEqual(record.Cleanup.CheckedTaskIDs, checkedBefore) {
		t.Fatal("Save mutated caller evidence")
	}

	got, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load saved ledger: %v", err)
	}
	if got.Format != "mobile-use-vnext-ledger" || got.Version != ledgerVersionV4 || got.Revision != 1 {
		t.Fatalf("saved header = %+v", got)
	}
	if !reflect.DeepEqual(got.UI, &uiBefore) ||
		!reflect.DeepEqual(got.LegacyUI, &checkpointBefore) ||
		!reflect.DeepEqual(got.Cleanup.CheckedTaskIDs, checkedBefore) {
		t.Fatalf("saved evidence did not round trip: %+v", got)
	}
}

func TestLedgerLegacyUIPendingRoundTrip(t *testing.T) {
	ledger := newTestLedgerStore(t)
	record := validTestLedger(2)
	record.LegacyUIPending = true
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"legacy_ui_pending":true`)) {
		t.Fatalf("pending marker was not encoded: %s", data)
	}
	if _, err := ledger.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.LegacyUIPending {
		t.Fatal("pending marker was lost")
	}

	loaded.LegacyUIPending = false
	if _, err := ledger.Save(context.Background(), loaded); err != nil {
		t.Fatalf("Save cleared marker: %v", err)
	}
	cleared, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load cleared marker: %v", err)
	}
	if cleared.LegacyUIPending {
		t.Fatal("cleared pending marker remained set")
	}
}

func TestLedgerRejectsV2NullLegacyUIPending(t *testing.T) {
	ledger := newTestLedgerStore(t)
	writeLedgerBytes(t, ledger, []byte(
		`{"format":"mobile-use-vnext-ledger","version":2,"revision":0,"legacy_ui_pending":null}`,
	))
	if _, err := ledger.Load(); err == nil {
		t.Fatal("explicit null legacy_ui_pending was accepted")
	}
}

func TestLedgerPhysicalRecheckEvidence(t *testing.T) {
	ledger := newTestLedgerStore(t)
	record := validTestLedger(2)
	record.Cleanup.ExecutionRecheckRequired = true
	if _, err := ledger.Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	loaded, err := ledger.Load()
	if err != nil || !loaded.Cleanup.ExecutionRecheckRequired {
		t.Fatal("physical recheck obligation did not survive a store round trip")
	}
	for _, body := range []string{
		`{"format":"mobile-use-vnext-ledger","version":2,"revision":0,"cleanup":{"execution_recheck_required":null}}`,
		`{"format":"mobile-use-vnext-ledger","version":1,"revision":0,"cleanup":{"execution_recheck_required":false}}`,
	} {
		ledger := newTestLedgerStore(t)
		writeLedgerBytes(t, ledger, []byte(body))
		if _, err := ledger.Load(); err == nil {
			t.Fatal("invalid physical recheck evidence was accepted")
		}
	}
}

func TestLedgerV1UpgradePreservesResponsibilities(t *testing.T) {
	ledger := newTestLedgerStore(t)
	want := validTestLedger(1)
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal v1 ledger: %v", err)
	}
	data = bytes.Replace(data, []byte(`,"legacy_ui_pending":false`), nil, 1)
	writeLedgerBytes(t, ledger, data)

	loaded, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.UI != nil || loaded.LegacyUI != nil || loaded.Cleanup.CheckedTaskIDs != nil {
		t.Fatalf("v1 load invented evidence: %+v", loaded)
	}
	if _, err := ledger.Save(context.Background(), loaded); err != nil {
		t.Fatalf("Save upgrade: %v", err)
	}
	got, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load upgraded ledger: %v", err)
	}
	if got.Version != ledgerVersionV4 || got.Revision != 8 {
		t.Fatalf("upgraded header = %+v", got)
	}
	for name, same := range map[string]bool{
		"session":        reflect.DeepEqual(got.Session, want.Session),
		"pending create": reflect.DeepEqual(got.PendingCreate, want.PendingCreate),
		"pending close":  reflect.DeepEqual(got.PendingClose, want.PendingClose),
		"cleanup":        reflect.DeepEqual(got.Cleanup, want.Cleanup),
		"tasks":          reflect.DeepEqual(got.Tasks, want.Tasks),
		"stop":           reflect.DeepEqual(got.Stop, want.Stop),
		"shell":          reflect.DeepEqual(got.Shell, want.Shell),
	} {
		if !same {
			t.Fatalf("upgrade lost %s responsibility", name)
		}
	}
}

func TestLedgerLoadRejectsInvalidHeaderOrVersionOneEvidence(t *testing.T) {
	baseUI := `{"operation_id":"Pinput","origin":"Pstart","epoch":1,"step":1,"kind":"tap","display_id":12,"display_generation":"display-generation","device_id":"device-a","boot_id":"` + testLedgerBootID + `","state":"may_dispatch"}`
	cases := []struct {
		name string
		body string
	}{
		{"version zero", `{"format":"mobile-use-vnext-ledger","version":0,"revision":0}`},
		{"unknown version", `{"format":"mobile-use-vnext-ledger","version":5,"revision":0}`},
		{"foreign format", `{"format":"other","version":2,"revision":0}`},
		{"negative revision", `{"format":"mobile-use-vnext-ledger","version":2,"revision":-1}`},
		{"v1 ui object", `{"format":"mobile-use-vnext-ledger","version":1,"revision":0,"ui":` + baseUI + `}`},
		{"v1 ui null", `{"format":"mobile-use-vnext-ledger","version":1,"revision":0,"ui":null}`},
		{"v1 checkpoint null", `{"format":"mobile-use-vnext-ledger","version":1,"revision":0,"legacy_ui_checkpoint":null}`},
		{"v1 pending true", `{"format":"mobile-use-vnext-ledger","version":1,"revision":0,"legacy_ui_pending":true}`},
		{"v1 pending false", `{"format":"mobile-use-vnext-ledger","version":1,"revision":0,"legacy_ui_pending":false}`},
		{"v1 pending null", `{"format":"mobile-use-vnext-ledger","version":1,"revision":0,"legacy_ui_pending":null}`},
		{"v1 checked tasks null", `{"format":"mobile-use-vnext-ledger","version":1,"revision":0,"cleanup":{"checked_task_ids":null}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ledger := newTestLedgerStore(t)
			writeLedgerBytes(t, ledger, []byte(tc.body))
			if _, err := ledger.Load(); err == nil {
				t.Fatal("invalid ledger was accepted")
			}
		})
	}
}

func TestLedgerRejectsMalformedV2Evidence(t *testing.T) {
	tooManyIDs := make([]int, 257)
	for i := range tooManyIDs {
		tooManyIDs[i] = i + 1
	}
	cases := []struct {
		name   string
		mutate func(*engine.Ledger)
	}{
		{"unknown kind", func(r *engine.Ledger) { r.UI.Kind = "hover" }},
		{"empty state", func(r *engine.Ledger) { r.UI.State = "" }},
		{"unknown state", func(r *engine.Ledger) { r.UI.State = "finished" }},
		{"invalid operation id", func(r *engine.Ledger) { r.UI.OperationID = "Oinput" }},
		{"long operation id", func(r *engine.Ledger) { r.UI.OperationID = "P" + strings.Repeat("a", 64) }},
		{"invalid origin", func(r *engine.Ledger) { r.UI.Origin = "Pinput!" }},
		{"long origin", func(r *engine.Ledger) { r.UI.Origin = "P" + strings.Repeat("b", 64) }},
		{"zero epoch", func(r *engine.Ledger) { r.UI.Epoch = 0 }},
		{"zero step", func(r *engine.Ledger) { r.UI.Step = 0 }},
		{"negative display", func(r *engine.Ledger) { r.UI.DisplayID = -1 }},
		{"empty display generation", func(r *engine.Ledger) { r.UI.DisplayGeneration = "" }},
		{"empty device id", func(r *engine.Ledger) { r.UI.DeviceID = "" }},
		{"long device id", func(r *engine.Ledger) { r.UI.DeviceID = strings.Repeat("d", 129) }},
		{"invalid boot id", func(r *engine.Ledger) { r.UI.BootID = strings.ToUpper(testLedgerBootID) }},
		{"checkpoint invalid origin", func(r *engine.Ledger) { r.LegacyUI.Origin = "legacy" }},
		{"checkpoint empty device", func(r *engine.Ledger) { r.LegacyUI.DeviceID = "" }},
		{"checkpoint invalid boot", func(r *engine.Ledger) { r.LegacyUI.BootID = "boot-id" }},
		{"zero checked task", func(r *engine.Ledger) { r.Cleanup.CheckedTaskIDs = []int{0} }},
		{"negative checked task", func(r *engine.Ledger) { r.Cleanup.CheckedTaskIDs = []int{-1} }},
		{"duplicate checked task", func(r *engine.Ledger) { r.Cleanup.CheckedTaskIDs = []int{41, 41} }},
		{"too many checked tasks", func(r *engine.Ledger) { r.Cleanup.CheckedTaskIDs = tooManyIDs }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ledger := newTestLedgerStore(t)
			record := validTestLedger(2)
			record.UI = validTestLedgerUI()
			record.LegacyUI = validTestLedgerCheckpoint()
			record.Cleanup.CheckedTaskIDs = []int{41, 57}
			tc.mutate(record)
			writeLedgerRecord(t, ledger, record)
			if _, err := ledger.Load(); err == nil {
				t.Fatal("malformed v2 evidence was accepted")
			}
		})
	}
}

func TestLedgerRoundTripsMaximumCheckedTaskIDs(t *testing.T) {
	ledger := newTestLedgerStore(t)
	record := validTestLedger(2)
	record.Cleanup.CheckedTaskIDs = make([]int, 256)
	for i := range record.Cleanup.CheckedTaskIDs {
		record.Cleanup.CheckedTaskIDs[i] = i + 1
	}
	if _, err := ledger.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.Cleanup.CheckedTaskIDs, record.Cleanup.CheckedTaskIDs) {
		t.Fatal("checked task order did not round trip")
	}
}

func TestLedgerSaveRejectsMalformedEvidenceWithoutWriting(t *testing.T) {
	ledger := newTestLedgerStore(t)
	record := validTestLedger(1)
	record.UI = validTestLedgerUI()
	record.UI.Kind = "hover"
	before := *record
	if _, err := ledger.Save(context.Background(), record); err == nil {
		t.Fatal("malformed evidence was saved")
	}
	if !reflect.DeepEqual(*record, before) {
		t.Fatal("failed Save mutated caller record")
	}
	if _, err := os.Stat(ledger.path); !os.IsNotExist(err) {
		t.Fatalf("failed Save wrote a ledger: %v", err)
	}
}

func TestLedgerLoadRejectsUnknownEvidenceMembers(t *testing.T) {
	record := validTestLedger(2)
	record.UI = validTestLedgerUI()
	record.LegacyUI = validTestLedgerCheckpoint()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for name, invalid := range map[string][]byte{
		"top-level command": bytes.Replace(data, []byte(`"ui":`), []byte(`"command":"secret","ui":`), 1),
		"ui text":           bytes.Replace(data, []byte(`"kind":"tap"`), []byte(`"kind":"tap","text":"secret"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			ledger := newTestLedgerStore(t)
			writeLedgerBytes(t, ledger, invalid)
			if _, err := ledger.Load(); err == nil {
				t.Fatal("unknown evidence member was accepted")
			}
		})
	}
}

func TestLedgerMalformedV2IsNotOverwritten(t *testing.T) {
	ledger := newTestLedgerStore(t)
	invalid := validTestLedger(2)
	invalid.UI = validTestLedgerUI()
	invalid.UI.Kind = "hover"
	before := writeLedgerRecord(t, ledger, invalid)
	if _, err := ledger.Load(); err == nil {
		t.Fatal("malformed ledger loaded")
	}
	if _, err := ledger.Save(context.Background(), validTestLedger(2)); err == nil {
		t.Fatal("Save overwrote malformed ledger")
	}
	after, err := os.ReadFile(ledger.path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("malformed ledger was overwritten")
	}
}

func TestLedgerRejectsOversizedSnapshot(t *testing.T) {
	ledger := newTestLedgerStore(t)
	record := validTestLedger(2)
	record.ServiceInstanceID = strings.Repeat("x", MaxLedgerBytes)
	if _, err := ledger.Save(context.Background(), record); err == nil {
		t.Fatal("oversized snapshot was saved")
	}
}
