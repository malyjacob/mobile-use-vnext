package store

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/engine"
)

func validTestTransfer(operationID, direction, state string, taskID int) engine.LedgerTransfer {
	return engine.LedgerTransfer{
		OperationID: operationID, Direction: direction, State: state,
		Epoch: 1, BootID: testLedgerBootID,
		AppInstanceID: "11111111-1111-4111-8111-111111111111",
		Package:       "com.example.app",
		LeafTaskID:    taskID, RootTaskID: taskID,
		SourceDisplayID: 0, SourceGeneration: "source-generation",
		TargetDisplayID: 12,
		CreatedAt:       time.Date(2026, 11, 10, 3, 4, 5, 0, time.UTC),
	}
}

func validTestV5Ledger() *engine.Ledger {
	record := validTestLedger(ledgerVersionV5)
	record.Revision = 1
	origin := 0
	record.Tasks = []engine.LedgerTask{
		{TaskID: 41, Package: "com.example.owned", DisplayID: 12, RootTaskID: 100, Ownership: "owned", Generation: "generation-owned", Proven: true},
		{TaskID: 42, Package: "com.example.borrowed", DisplayID: 12, RootTaskID: 100, Ownership: "borrowed", OriginDisplay: &origin, Generation: "generation-borrowed", Proven: true},
		{TaskID: 43, Package: "com.example.shared", DisplayID: 0, Ownership: "shared", Generation: "generation-shared", Proven: true},
	}
	states := []string{
		engine.TransferPrepared, engine.TransferMayDispatch, engine.TransferTerminated,
		engine.TransferNotDispatched, engine.TransferUnknown, engine.TransferReleased,
	}
	for i, state := range states {
		direction := engine.TransferIn
		if i%2 == 1 {
			direction = engine.TransferOut
		}
		record.TaskTransfers = append(record.TaskTransfers,
			validTestTransfer("Ptransfer"+strconv.Itoa(i), direction, state, 100+i))
	}
	return record
}

func TestLedgerV5RoundTrip(t *testing.T) {
	ledger := newTestLedgerStore(t)
	want := validTestV5Ledger()
	if _, err := ledger.Save(context.Background(), want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("v5 round trip mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
	if got.Tasks[1].OriginDisplay == nil || *got.Tasks[1].OriginDisplay != 0 {
		t.Fatalf("borrowed origin display was lost: %+v", got.Tasks[1])
	}
	if len(got.TaskTransfers) != len(want.TaskTransfers) {
		t.Fatalf("transfers = %d, want %d", len(got.TaskTransfers), len(want.TaskTransfers))
	}
	for i := range got.TaskTransfers {
		if got.TaskTransfers[i].State != want.TaskTransfers[i].State {
			t.Fatalf("transfer %d state = %q, want %q", i, got.TaskTransfers[i].State, want.TaskTransfers[i].State)
		}
	}
}

func TestLedgerV5SaveNeverDowngrades(t *testing.T) {
	ledger := newTestLedgerStore(t)
	record := validTestV5Ledger()
	if _, err := ledger.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded.Version = ledgerVersionV4
	if _, err := ledger.Save(context.Background(), loaded); err != nil {
		t.Fatalf("Save loaded v5: %v", err)
	}
	data, err := os.ReadFile(ledger.path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if !bytes.Contains(data, []byte(`"version":5`)) || bytes.Contains(data, []byte(`"version":4`)) {
		t.Fatalf("saved ledger is not v5: %s", data)
	}
	again, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load saved v5: %v", err)
	}
	if again.Version != ledgerVersionV5 {
		t.Fatalf("version = %d, want %d", again.Version, ledgerVersionV5)
	}
	if !reflect.DeepEqual(again.Tasks, loaded.Tasks) || !reflect.DeepEqual(again.TaskTransfers, loaded.TaskTransfers) {
		t.Fatal("saving again dropped v5 fields")
	}
}

func TestLedgerV5CloneDeepCopiesTaskFieldsAndTransfers(t *testing.T) {
	original := validTestV5Ledger()
	retired := validTestRetired(0)
	origin := 0
	retired.Record.Tasks = []engine.LedgerTask{{
		TaskID: 55, Package: "com.example.retired", DisplayID: 12, RootTaskID: 100,
		Ownership: "borrowed", OriginDisplay: &origin, Generation: "retired-generation", Proven: true,
	}}
	original.Retired = []engine.LedgerRetired{retired}

	clone := engine.CloneLedger(original)
	want := engine.CloneLedger(clone)
	if clone.Tasks[1].OriginDisplay == original.Tasks[1].OriginDisplay {
		t.Fatal("clone shares a task origin display pointer")
	}
	if len(clone.TaskTransfers) == 0 || &clone.TaskTransfers[0] == &original.TaskTransfers[0] {
		t.Fatal("clone shares the transfer backing array")
	}
	if clone.Retired[0].Record.Tasks[0].OriginDisplay == original.Retired[0].Record.Tasks[0].OriginDisplay {
		t.Fatal("clone shares a nested retired task origin display pointer")
	}

	original.Tasks[1].Package = "changed.package"
	*original.Tasks[1].OriginDisplay = 9
	original.TaskTransfers[0].Package = "changed.transfer"
	original.TaskTransfers[0].State = engine.TransferReleased
	original.Retired[0].Record.Tasks[0].Generation = "changed-generation"

	if !reflect.DeepEqual(clone, want) {
		t.Fatalf("CloneLedger changed with its source\ngot:  %#v\nwant: %#v", clone, want)
	}
	if clone.Tasks[1].Package != "com.example.borrowed" ||
		*clone.Tasks[1].OriginDisplay != 0 ||
		clone.TaskTransfers[0].Package != "com.example.app" ||
		clone.TaskTransfers[0].State != engine.TransferPrepared ||
		clone.Retired[0].Record.Tasks[0].Generation != "retired-generation" {
		t.Fatal("clone retained source mutations")
	}
}

func TestLedgerOlderVersionsRejectV5Evidence(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4} {
		for _, tc := range []struct {
			name   string
			mutate func(*engine.Ledger)
		}{
			{"task transfers", func(record *engine.Ledger) {
				record.TaskTransfers = []engine.LedgerTransfer{validTestTransfer("Polder", engine.TransferIn, engine.TransferPrepared, 41)}
			}},
			{"task root", func(record *engine.Ledger) { record.Tasks[0].RootTaskID = 100 }},
			{"task ownership", func(record *engine.Ledger) { record.Tasks[0].Ownership = "owned" }},
			{"task origin", func(record *engine.Ledger) {
				origin := 0
				record.Tasks[0].OriginDisplay = &origin
			}},
			{"task generation", func(record *engine.Ledger) { record.Tasks[0].Generation = "generation" }},
			{"nested retired task transfers", func(record *engine.Ledger) {
				retired := validTestRetired(0)
				retired.Record.TaskTransfers = []engine.LedgerTransfer{validTestTransfer("Pnested", engine.TransferIn, engine.TransferPrepared, 41)}
				record.Retired = []engine.LedgerRetired{retired}
			}},
		} {
			t.Run(fmt.Sprintf("v%d/%s", version, tc.name), func(t *testing.T) {
				ledger := newTestLedgerStore(t)
				record := validTestLedger(version)
				tc.mutate(record)
				writeLedgerRecord(t, ledger, record)
				if _, err := ledger.Load(); err == nil {
					t.Fatal("older ledger accepted version 5 evidence")
				}
			})
		}
	}
}

func TestHasVersionFiveEvidence(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"task transfers", `{"task_transfers":[]}`, true},
		{"null task transfers", `{"task_transfers":null}`, true},
		{"task root zero", `{"tasks":[{"root_task_id":0}]}`, true},
		{"task ownership", `{"tasks":[{"ownership":"borrowed"}]}`, true},
		{"task origin", `{"tasks":[{"origin_display":0}]}`, true},
		{"task generation", `{"tasks":[{"generation":"g"}]}`, true},
		{"old task", `{"tasks":[{"task_id":41,"package":"p","display_id":12,"proven":true}]}`, false},
		{"nested transfers", `{"retired":[{"record":{"task_transfers":[]}}]}`, true},
		{"nested task ownership", `{"retired":[{"record":{"tasks":[{"ownership":"shared"}]}}]}`, true},
		{"no evidence", `{"format":"mobile-use-vnext-ledger","version":4,"revision":0}`, false},
		{"malformed", `{`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasVersionFiveEvidence([]byte(tc.body)); got != tc.want {
				t.Fatalf("hasVersionFiveEvidence(%s) = %t, want %t", tc.body, got, tc.want)
			}
		})
	}
}

func TestLedgerV5RetiredRejectsTaskTransfers(t *testing.T) {
	record := validTestLedger(ledgerVersionV5)
	retired := validTestRetired(0)
	retired.Record.TaskTransfers = []engine.LedgerTransfer{validTestTransfer("Pnested", engine.TransferIn, engine.TransferPrepared, 41)}
	record.Retired = []engine.LedgerRetired{retired}

	ledger := newTestLedgerStore(t)
	if _, err := ledger.Save(context.Background(), record); err == nil {
		t.Fatal("Save accepted retired task transfers")
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	writeLedgerBytes(t, ledger, data)
	before, err := os.ReadFile(ledger.path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if _, err := ledger.Load(); err == nil {
		t.Fatal("Load accepted retired task transfers")
	}
	after, err := os.ReadFile(ledger.path)
	if err != nil {
		t.Fatalf("read ledger after rejection: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected ledger was rewritten")
	}
}

func TestLedgerV5RejectsNullTaskTransfers(t *testing.T) {
	ledger := newTestLedgerStore(t)
	writeLedgerBytes(t, ledger, []byte(`{"format":"mobile-use-vnext-ledger","version":5,"revision":0,"task_transfers":null}`))
	if _, err := ledger.Load(); err == nil {
		t.Fatal("explicit null task_transfers was accepted")
	}
}

func ledgerWithBaseSize(t *testing.T, size int) *engine.Ledger {
	t.Helper()
	record := validTestLedger(ledgerVersionV5)
	record.Retired = nil
	record.TaskTransfers = nil
	record.ServiceInstanceID = ""
	base, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal base: %v", err)
	}
	padding := size - len(base)
	if padding < 0 {
		t.Fatalf("base record is larger than %d bytes", size)
	}
	record.ServiceInstanceID = strings.Repeat("b", padding)
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal padded base: %v", err)
	}
	if len(data) != size {
		t.Fatalf("base size = %d, want %d", len(data), size)
	}
	return record
}

func transferWithSerializedSize(t *testing.T, size int) engine.LedgerTransfer {
	t.Helper()
	transfer := validTestTransfer("Pentry", engine.TransferIn, engine.TransferPrepared, 41)
	transfer.Package = ""
	base, err := json.Marshal(transfer)
	if err != nil {
		t.Fatalf("marshal transfer: %v", err)
	}
	padding := size - len(base)
	if padding < 0 {
		t.Fatalf("transfer is larger than %d bytes", size)
	}
	transfer.Package = strings.Repeat("p", padding)
	data, err := json.Marshal(transfer)
	if err != nil {
		t.Fatalf("marshal padded transfer: %v", err)
	}
	if len(data) != size {
		t.Fatalf("transfer size = %d, want %d", len(data), size)
	}
	return transfer
}

func TestLedgerV5TaskTransferCountAndEntryBudgets(t *testing.T) {
	atLimit := validTestLedger(ledgerVersionV5)
	for i := 0; i < maxTaskTransfers; i++ {
		atLimit.TaskTransfers = append(atLimit.TaskTransfers,
			validTestTransfer("Pcount"+strconv.Itoa(i), engine.TransferIn, engine.TransferPrepared, 100+i))
	}
	accepted := newTestLedgerStore(t)
	if _, err := accepted.Save(context.Background(), atLimit); err != nil {
		t.Fatalf("Save %d transfers: %v", maxTaskTransfers, err)
	}
	if _, err := accepted.Load(); err != nil {
		t.Fatalf("Load %d transfers: %v", maxTaskTransfers, err)
	}

	tooMany := engine.CloneLedger(atLimit)
	tooMany.TaskTransfers = append(tooMany.TaskTransfers, validTestTransfer("Pcount-extra", engine.TransferIn, engine.TransferPrepared, 999))
	rejected := newTestLedgerStore(t)
	if _, err := rejected.Save(context.Background(), tooMany); err == nil {
		t.Fatal("Save accepted more than the transfer count limit")
	}
	writeLedgerRecord(t, rejected, tooMany)
	if _, err := rejected.Load(); err == nil {
		t.Fatal("Load accepted more than the transfer count limit")
	}

	entryAtLimit := validTestLedger(ledgerVersionV5)
	entryAtLimit.TaskTransfers = []engine.LedgerTransfer{transferWithSerializedSize(t, MaxTaskTransferEntryBytes)}
	entryStore := newTestLedgerStore(t)
	if _, err := entryStore.Save(context.Background(), entryAtLimit); err != nil {
		t.Fatalf("Save transfer at the entry limit: %v", err)
	}
	if _, err := entryStore.Load(); err != nil {
		t.Fatalf("Load transfer at the entry limit: %v", err)
	}

	entryOver := validTestLedger(ledgerVersionV5)
	entryOver.TaskTransfers = []engine.LedgerTransfer{transferWithSerializedSize(t, MaxTaskTransferEntryBytes+1)}
	entryOverStore := newTestLedgerStore(t)
	if _, err := entryOverStore.Save(context.Background(), entryOver); err == nil {
		t.Fatal("Save accepted a transfer over the entry limit")
	}
	writeLedgerRecord(t, entryOverStore, entryOver)
	if _, err := entryOverStore.Load(); err == nil {
		t.Fatal("Load accepted a transfer over the entry limit")
	}
}

func TestLedgerV5TaskTransferArrayBudget(t *testing.T) {
	// The per-entry bound keeps a full sixteen-entry array below the array
	// bound. Saturate both dimensions and prove the aggregate still passes.
	record := validTestLedger(ledgerVersionV5)
	for i := 0; i < maxTaskTransfers; i++ {
		record.TaskTransfers = append(record.TaskTransfers, transferWithSerializedSize(t, MaxTaskTransferEntryBytes))
	}
	data, err := json.Marshal(record.TaskTransfers)
	if err != nil {
		t.Fatalf("marshal transfers: %v", err)
	}
	if len(data) > MaxTaskTransferBytes {
		t.Fatalf("transfer array = %d bytes, want <= %d", len(data), MaxTaskTransferBytes)
	}
	ledger := newTestLedgerStore(t)
	if _, err := ledger.Save(context.Background(), record); err != nil {
		t.Fatalf("Save saturated transfer array: %v", err)
	}
	if _, err := ledger.Load(); err != nil {
		t.Fatalf("Load saturated transfer array: %v", err)
	}
}

func TestLedgerV5BaseBudget(t *testing.T) {
	atLimit := ledgerWithBaseSize(t, MaxBaseBytes)
	accepted := newTestLedgerStore(t)
	if _, err := accepted.Save(context.Background(), atLimit); err != nil {
		t.Fatalf("Save base at the limit: %v", err)
	}
	if _, err := accepted.Load(); err != nil {
		t.Fatalf("Load base at the limit: %v", err)
	}

	over := ledgerWithBaseSize(t, MaxBaseBytes+1)
	rejected := newTestLedgerStore(t)
	if _, err := rejected.Save(context.Background(), over); err == nil {
		t.Fatal("Save accepted a base over the limit")
	}
	writeLedgerRecord(t, rejected, over)
	if _, err := rejected.Load(); err == nil {
		t.Fatal("Load accepted a base over the limit")
	}
}

func TestLedgerV5RetiredBudgets(t *testing.T) {
	atLimit := validTestLedger(ledgerVersionV5)
	for i := 0; i < maxRetiredEntries; i++ {
		atLimit.Retired = append(atLimit.Retired, validTestRetired(i%4))
	}
	accepted := newTestLedgerStore(t)
	if _, err := accepted.Save(context.Background(), atLimit); err != nil {
		t.Fatalf("Save %d retired entries: %v", maxRetiredEntries, err)
	}
	if _, err := accepted.Load(); err != nil {
		t.Fatalf("Load %d retired entries: %v", maxRetiredEntries, err)
	}

	tooMany := engine.CloneLedger(atLimit)
	tooMany.Retired = append(tooMany.Retired, validTestRetired(0))
	rejected := newTestLedgerStore(t)
	if _, err := rejected.Save(context.Background(), tooMany); err == nil {
		t.Fatal("Save accepted more than the retired count limit")
	}
	writeLedgerRecord(t, rejected, tooMany)
	if _, err := rejected.Load(); err == nil {
		t.Fatal("Load accepted more than the retired count limit")
	}

	exact, err := retiredWithSerializedSize(t, MaxRetiredBytes)
	if err != nil {
		t.Fatal(err)
	}
	exactRecord := validTestLedger(ledgerVersionV5)
	exactRecord.Retired = []engine.LedgerRetired{exact}
	exactStore := newTestLedgerStore(t)
	if _, err := exactStore.Save(context.Background(), exactRecord); err != nil {
		t.Fatalf("Save retired entries at the size limit: %v", err)
	}
	if _, err := exactStore.Load(); err != nil {
		t.Fatalf("Load retired entries at the size limit: %v", err)
	}

	over, err := retiredWithSerializedSize(t, MaxRetiredBytes+1)
	if err != nil {
		t.Fatal(err)
	}
	overRecord := validTestLedger(ledgerVersionV5)
	overRecord.Retired = []engine.LedgerRetired{over}
	overStore := newTestLedgerStore(t)
	if _, err := overStore.Save(context.Background(), overRecord); err == nil {
		t.Fatal("Save accepted retired entries over the size limit")
	}
	writeLedgerRecord(t, overStore, overRecord)
	if _, err := overStore.Load(); err == nil {
		t.Fatal("Load accepted retired entries over the size limit")
	}
}

func TestLedgerV5ComposedBudgetsStayUnderTotal(t *testing.T) {
	record := ledgerWithBaseSize(t, MaxBaseBytes)
	exact, err := retiredWithSerializedSize(t, MaxRetiredBytes)
	if err != nil {
		t.Fatal(err)
	}
	record.Retired = []engine.LedgerRetired{exact}
	for i := 0; i < maxTaskTransfers; i++ {
		record.TaskTransfers = append(record.TaskTransfers, transferWithSerializedSize(t, MaxTaskTransferEntryBytes))
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal composed ledger: %v", err)
	}
	if len(data) > MaxLedgerBytes {
		t.Fatalf("composed ledger = %d bytes, want <= %d", len(data), MaxLedgerBytes)
	}
	ledger := newTestLedgerStore(t)
	if _, err := ledger.Save(context.Background(), record); err != nil {
		t.Fatalf("Save composed ledger: %v", err)
	}
	if _, err := ledger.Load(); err != nil {
		t.Fatalf("Load composed ledger: %v", err)
	}
}

func TestLedgerV5TotalFileBudget(t *testing.T) {
	record := validTestLedger(ledgerVersionV5)
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) > MaxLedgerBytes {
		t.Fatalf("base record = %d bytes, want <= %d", len(data), MaxLedgerBytes)
	}
	atLimit := append(append([]byte{}, data...), bytes.Repeat([]byte(" "), MaxLedgerBytes-len(data))...)
	accepted := newTestLedgerStore(t)
	writeLedgerBytes(t, accepted, atLimit)
	if _, err := accepted.Load(); err != nil {
		t.Fatalf("Load file at the total limit: %v", err)
	}

	overLimit := append(append([]byte{}, atLimit...), ' ')
	rejected := newTestLedgerStore(t)
	writeLedgerBytes(t, rejected, overLimit)
	if _, err := rejected.Load(); err == nil {
		t.Fatal("Load accepted a file over the total limit")
	}
}

func TestLedgerV5RejectedByV4ReaderWithoutRewrite(t *testing.T) {
	ledger := newTestLedgerStore(t)
	record := validTestV5Ledger()
	if _, err := ledger.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	before, err := os.ReadFile(ledger.path)
	if err != nil {
		t.Fatalf("read saved ledger: %v", err)
	}
	if !bytes.Contains(before, []byte(`"version":5`)) {
		t.Fatalf("saved ledger does not declare version 5: %s", before)
	}
	if _, err := decodeLedger(before, ledgerVersionV4); err == nil {
		t.Fatal("version 4 validation accepted a version 5 ledger")
	}
	legacy := bytes.Replace(before, []byte(`"version":5`), []byte(`"version":4`), 1)
	legacyStore := newTestLedgerStore(t)
	writeLedgerBytes(t, legacyStore, legacy)
	if _, err := legacyStore.Load(); err == nil {
		t.Fatal("version 4 branch accepted a ledger containing version 5 evidence")
	}
	legacyAfter, err := os.ReadFile(legacyStore.path)
	if err != nil {
		t.Fatalf("read legacy-labelled ledger after rejection: %v", err)
	}
	if !bytes.Equal(legacy, legacyAfter) {
		t.Fatal("rejected legacy-labelled ledger was rewritten")
	}
	after, err := os.ReadFile(ledger.path)
	if err != nil {
		t.Fatalf("read ledger after rejection: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("old-version rejection rewrote the ledger")
	}
	if _, err := ledger.Load(); err != nil {
		t.Fatalf("Load current version: %v", err)
	}
	afterCurrent, err := os.ReadFile(ledger.path)
	if err != nil {
		t.Fatalf("read ledger after current load: %v", err)
	}
	if !bytes.Equal(before, afterCurrent) {
		t.Fatal("Load rewrote the ledger")
	}
}

func TestLedgerV5EncodingHasNoRequestTreeOrFreeText(t *testing.T) {
	record := validTestV5Ledger()
	ledger := newTestLedgerStore(t)
	if _, err := ledger.Save(context.Background(), record); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(ledger.path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	forbidden := map[string]bool{
		"request": true, "tree": true, "text": true, "input": true,
		"output": true, "body": true, "message": true, "command": true,
		"content": true, "nodes": true,
	}
	keys := make(map[string]bool)
	collectJSONKeys(t, data, keys)
	for key := range keys {
		if forbidden[key] {
			t.Fatalf("ledger contains forbidden free-text member %q: %s", key, data)
		}
	}
	for _, secret := range []string{"request-body-secret", "tree-node-secret", "free-text-secret"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("ledger contains free text %q", secret)
		}
	}
}

func collectJSONKeys(t *testing.T, data []byte, keys map[string]bool) {
	t.Helper()
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return
	}
	switch trimmed[0] {
	case '{':
		var object map[string]jsontext.Value
		if err := json.Unmarshal(trimmed, &object); err != nil {
			t.Fatalf("decode object: %v", err)
		}
		for key, value := range object {
			keys[key] = true
			collectJSONKeys(t, value, keys)
		}
	case '[':
		var array []jsontext.Value
		if err := json.Unmarshal(trimmed, &array); err != nil {
			t.Fatalf("decode array: %v", err)
		}
		for _, value := range array {
			collectJSONKeys(t, value, keys)
		}
	}
}
