package store

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/engine"
)

func validTestRetired(attempts int) engine.LedgerRetired {
	record := validTestLedger(3)
	record.Retired = nil
	record.UI = validTestLedgerUI()
	record.Cleanup.CheckedTaskIDs = []int{41, 57}
	record.Shell.ExitCode = new(int)
	*record.Shell.ExitCode = 0
	record.Shell.Truncated = new(bool)
	return engine.LedgerRetired{
		Record:       record,
		BudgetIssued: true,
		Attempts:     attempts,
	}
}

func TestLedgerV3RetiredRoundTrip(t *testing.T) {
	ledger := newTestLedgerStore(t)
	want := validTestLedger(ledgerVersionV4)
	want.Revision = 1
	retired := validTestRetired(0)
	retired.BudgetIssued = false
	retired.StoppedReason = "budget_exhausted"
	want.Retired = []engine.LedgerRetired{retired}

	if _, err := ledger.Save(context.Background(), want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(ledger.path)
	if err != nil {
		t.Fatalf("read saved ledger: %v", err)
	}
	for _, member := range [][]byte{
		[]byte(`"version":4`),
		[]byte(`"create_command":"55555555-5555-4555-8555-555555555555"`),
		[]byte(`"device_id":"device-private-a"`),
		[]byte(`"boot_id":"` + testLedgerBootID + `"`),
		[]byte(`"budget_issued":false`),
		[]byte(`"stopped_reason":"budget_exhausted"`),
		[]byte(`"attempts":0`),
	} {
		if !bytes.Contains(data, member) {
			t.Fatalf("v3 encoding omitted %s: %s", member, data)
		}
	}

	got, err := ledger.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
	if got.Retired[0].Record == want.Retired[0].Record {
		t.Fatal("loaded retired record aliases caller evidence")
	}
	if got.Retired[0].Record.Retired != nil {
		t.Fatal("loaded retired record became recursive")
	}
}

func TestLedgerCloneDeepCopiesNestedEvidence(t *testing.T) {
	original := validTestLedger(3)
	original.Cleanup.CheckedTaskIDs = []int{41, 57}
	original.Retired = []engine.LedgerRetired{validTestRetired(1)}

	clone := engine.CloneLedger(original)
	want := engine.CloneLedger(clone)
	if clone == original ||
		clone.Session == original.Session ||
		clone.Cleanup == original.Cleanup ||
		clone.Shell == original.Shell ||
		clone.Retired[0].Record == original.Retired[0].Record ||
		clone.Retired[0].Record.Session == original.Retired[0].Record.Session {
		t.Fatal("CloneLedger retained a nested alias")
	}

	original.Session.Agent = "changed"
	original.Cleanup.CheckedTaskIDs[0] = 99
	original.Tasks[0].Package = "changed.package"
	original.Shell.ExitCode = new(int)
	*original.Shell.ExitCode = 7
	original.Retired[0].Record.Session.DeviceID = "changed-device"
	original.Retired[0].Record.Cleanup.CheckedTaskIDs[1] = 88

	if !reflect.DeepEqual(clone, want) {
		t.Fatalf("CloneLedger changed with its source\ngot:  %#v\nwant: %#v", clone, want)
	}
}

func TestLedgerV1AndV2AllowMissingCrossBootIdentity(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run("v"+string(rune('0'+version)), func(t *testing.T) {
			ledger := newTestLedgerStore(t)
			record := validTestLedger(version)
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatalf("marshal v%d: %v", version, err)
			}
			if version == 1 {
				data = bytes.Replace(data, []byte(`,"legacy_ui_pending":false`), nil, 1)
			}
			writeLedgerBytes(t, ledger, data)

			loaded, err := ledger.Load()
			if err != nil {
				t.Fatalf("Load v%d: %v", version, err)
			}
			if loaded.Session.CreateCommand != "" ||
				loaded.Session.DeviceID != "" ||
				loaded.Session.BootID != "" {
				t.Fatalf("v%d load invented cross-boot identity: %+v", version, loaded.Session)
			}
			if _, err := ledger.Save(context.Background(), loaded); err != nil {
				t.Fatalf("Save v%d migration: %v", version, err)
			}
			upgraded, err := ledger.Load()
			if err != nil {
				t.Fatalf("Load migrated v%d: %v", version, err)
			}
			if upgraded.Version != ledgerVersionV4 ||
				upgraded.Session.CreateCommand != "" ||
				upgraded.Session.DeviceID != "" ||
				upgraded.Session.BootID != "" {
				t.Fatalf("v%d migration changed old facts: %+v", version, upgraded)
			}
		})
	}
}

func TestLedgerOlderVersionsRejectV3Evidence(t *testing.T) {
	cases := []struct {
		name   string
		record *engine.Ledger
	}{
		{
			name: "v1 session identity",
			record: func() *engine.Ledger {
				record := validTestLedger(1)
				record.Session.CreateCommand = "55555555-5555-4555-8555-555555555555"
				return record
			}(),
		},
		{
			name: "v1 retired",
			record: func() *engine.Ledger {
				record := validTestLedger(1)
				record.Retired = []engine.LedgerRetired{validTestRetired(0)}
				return record
			}(),
		},
		{
			name: "v2 session identity",
			record: func() *engine.Ledger {
				record := validTestLedger(2)
				record.Session.BootID = testLedgerBootID
				return record
			}(),
		},
		{
			name: "v2 retired",
			record: func() *engine.Ledger {
				record := validTestLedger(2)
				record.Retired = []engine.LedgerRetired{validTestRetired(0)}
				return record
			}(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ledger := newTestLedgerStore(t)
			writeLedgerRecord(t, ledger, tc.record)
			if _, err := ledger.Load(); err == nil {
				t.Fatal("older ledger accepted version 3 evidence")
			}
		})
	}
}

func TestLedgerRejectsInvalidRetiredRecords(t *testing.T) {
	tooMany := make([]engine.LedgerRetired, 9)
	for i := range tooMany {
		tooMany[i] = validTestRetired(i % 4)
	}
	cases := []struct {
		name   string
		mutate func(*engine.Ledger)
	}{
		{"too many entries", func(record *engine.Ledger) { record.Retired = tooMany }},
		{"nil nested record", func(record *engine.Ledger) {
			record.Retired = []engine.LedgerRetired{{BudgetIssued: true}}
		}},
		{"negative attempts", func(record *engine.Ledger) {
			record.Retired = []engine.LedgerRetired{validTestRetired(-1)}
		}},
		{"too many attempts", func(record *engine.Ledger) {
			record.Retired = []engine.LedgerRetired{validTestRetired(4)}
		}},
		{"recursive retirement", func(record *engine.Ledger) {
			retired := validTestRetired(0)
			retired.Record.Retired = []engine.LedgerRetired{}
			record.Retired = []engine.LedgerRetired{retired}
		}},
		{"invalid boot identity", func(record *engine.Ledger) {
			record.Session.BootID = "boot-id"
		}},
		{"long device identity", func(record *engine.Ledger) {
			record.Session.DeviceID = strings.Repeat("d", maxDeviceIDLen+1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ledger := newTestLedgerStore(t)
			record := validTestLedger(3)
			tc.mutate(record)
			if _, err := ledger.Save(context.Background(), record); err == nil {
				t.Fatal("invalid retired record was saved")
			}
			if _, err := os.Stat(ledger.path); !os.IsNotExist(err) {
				t.Fatalf("failed Save wrote a ledger: %v", err)
			}
		})
	}
}

func TestLedgerRejectsInvalidRetiredJSON(t *testing.T) {
	const prefix = `{"format":"mobile-use-vnext-ledger","version":3,"revision":0,"retired":`
	cases := map[string]string{
		"null retired":        prefix + `null}`,
		"null record":         prefix + `[{"record":null,"budget_issued":false,"attempts":0}]}`,
		"missing record":      prefix + `[{"budget_issued":false,"attempts":0}]}`,
		"null budget marker":  prefix + `[{"record":{},"budget_issued":null,"attempts":0}]}`,
		"missing budget":      prefix + `[{"record":{},"attempts":0}]}`,
		"null attempts":       prefix + `[{"record":{},"budget_issued":false,"attempts":null}]}`,
		"missing attempts":    prefix + `[{"record":{},"budget_issued":false}]}`,
		"negative attempts":   prefix + `[{"record":{},"budget_issued":false,"attempts":-1}]}`,
		"too many attempts":   prefix + `[{"record":{},"budget_issued":false,"attempts":4}]}`,
		"null stop reason":    prefix + `[{"record":{},"budget_issued":false,"stopped_reason":null,"attempts":0}]}`,
		"null recursion":      prefix + `[{"record":{"retired":null},"budget_issued":false,"attempts":0}]}`,
		"empty recursion":     prefix + `[{"record":{"retired":[]},"budget_issued":false,"attempts":0}]}`,
		"unknown entry":       prefix + `[{"record":{},"budget_issued":false,"attempts":0,"command":"secret"}]}`,
		"case variant":        prefix + `[{"record":{},"budget_issued":false,"BudgetIssued":true,"attempts":0}]}`,
		"unknown nested":      prefix + `[{"record":{"session":{"command":"secret"}},"budget_issued":false,"attempts":0}]}`,
		"null session device": prefix + `[{"record":{"session":{"device_id":null}},"budget_issued":false,"attempts":0}]}`,
		"null nested boolean": prefix + `[{"record":{"legacy_ui_pending":null},"budget_issued":false,"attempts":0}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			ledger := newTestLedgerStore(t)
			writeLedgerBytes(t, ledger, []byte(body))
			if _, err := ledger.Load(); err == nil {
				t.Fatal("invalid retired JSON was accepted")
			}
		})
	}
}

func TestLedgerRejectsNullCurrentCrossBootIdentity(t *testing.T) {
	for name, body := range map[string]string{
		"create command": `{"format":"mobile-use-vnext-ledger","version":3,"revision":0,"session":{"create_command":null}}`,
		"device":         `{"format":"mobile-use-vnext-ledger","version":3,"revision":0,"session":{"device_id":null}}`,
		"boot":           `{"format":"mobile-use-vnext-ledger","version":3,"revision":0,"session":{"boot_id":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ledger := newTestLedgerStore(t)
			writeLedgerBytes(t, ledger, []byte(body))
			if _, err := ledger.Load(); err == nil {
				t.Fatal("explicit null cross-boot identity was accepted")
			}
		})
	}
}

func TestLedgerRetiredCapacityBoundaries(t *testing.T) {
	eight := validTestLedger(3)
	for i := 0; i < maxRetiredEntries; i++ {
		eight.Retired = append(eight.Retired, validTestRetired(i%4))
	}
	accepted := newTestLedgerStore(t)
	if _, err := accepted.Save(context.Background(), eight); err != nil {
		t.Fatalf("Save eight retired entries: %v", err)
	}
	if _, err := accepted.Load(); err != nil {
		t.Fatalf("Load eight retired entries: %v", err)
	}

	tooMany := engine.CloneLedger(eight)
	tooMany.Retired = append(tooMany.Retired, validTestRetired(0))
	rejected := newTestLedgerStore(t)
	if _, err := rejected.Save(context.Background(), tooMany); err == nil {
		t.Fatal("Save accepted more than eight retired entries")
	}
	writeLedgerRecord(t, rejected, tooMany)
	if _, err := rejected.Load(); err == nil {
		t.Fatal("Load accepted more than eight retired entries")
	}

	exact, err := retiredWithSerializedSize(t, MaxRetiredBytes)
	if err != nil {
		t.Fatal(err)
	}
	atLimit := validTestLedger(3)
	atLimit.Retired = []engine.LedgerRetired{exact}
	atLimitStore := newTestLedgerStore(t)
	if _, err := atLimitStore.Save(context.Background(), atLimit); err != nil {
		t.Fatalf("Save retired entries at the size limit: %v", err)
	}
	writeLedgerRecord(t, atLimitStore, atLimit)
	if _, err := atLimitStore.Load(); err != nil {
		t.Fatalf("Load retired entries at the size limit: %v", err)
	}

	over, err := retiredWithSerializedSize(t, MaxRetiredBytes+1)
	if err != nil {
		t.Fatal(err)
	}
	overLimit := validTestLedger(3)
	overLimit.Retired = []engine.LedgerRetired{over}
	overLimitStore := newTestLedgerStore(t)
	if _, err := overLimitStore.Save(context.Background(), overLimit); err == nil {
		t.Fatal("Save accepted retired entries over the size limit")
	}
	writeLedgerRecord(t, overLimitStore, overLimit)
	if _, err := overLimitStore.Load(); err == nil {
		t.Fatal("Load accepted retired entries over the size limit")
	}
}

func retiredWithSerializedSize(t *testing.T, size int) (engine.LedgerRetired, error) {
	t.Helper()
	retired := validTestRetired(3)
	retired.Record.ServiceInstanceID = ""
	base, err := json.Marshal([]engine.LedgerRetired{retired})
	if err != nil {
		return engine.LedgerRetired{}, err
	}
	padding := size - len(base)
	if padding < 0 {
		t.Fatalf("base retired record is larger than %d bytes", size)
	}
	retired.Record.ServiceInstanceID = strings.Repeat("r", padding)
	return retired, nil
}

func TestLedgerFullSizeBudgetIncludesRetired(t *testing.T) {
	ledger := newTestLedgerStore(t)
	record := validTestLedger(3)
	record.ServiceInstanceID = strings.Repeat("c", 45*1024)
	retired := validTestRetired(0)
	retired.Record.ServiceInstanceID = strings.Repeat("r", 20*1024)
	record.Retired = []engine.LedgerRetired{retired}

	if _, err := ledger.Save(context.Background(), record); err == nil {
		t.Fatal("oversized complete ledger was saved")
	}
	writeLedgerRecord(t, ledger, record)
	if _, err := ledger.Load(); err == nil {
		t.Fatal("oversized complete ledger was loaded")
	}
}
