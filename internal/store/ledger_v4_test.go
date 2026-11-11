package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/engine"
)

func TestLedgerV4UnknownUIRoundTrip(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%t", nested), func(t *testing.T) {
			ledger := newTestLedgerStore(t)
			want := validTestLedger(4)
			if nested {
				retired := validTestRetired(0)
				retired.Record.UI.State = "unknown"
				want.Retired = []engine.LedgerRetired{retired}
			} else {
				want.UI = validTestLedgerUI()
				want.UI.State = "unknown"
			}
			writeLedgerRecord(t, ledger, want)

			loaded, err := ledger.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !reflect.DeepEqual(loaded, want) {
				t.Fatalf("loaded evidence mismatch\ngot:  %#v\nwant: %#v", loaded, want)
			}
			revision, err := ledger.Save(context.Background(), loaded)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			want.Revision++
			if revision != want.Revision || ledger.Revision() != want.Revision {
				t.Fatalf("revision = %d, store revision = %d, want %d", revision, ledger.Revision(), want.Revision)
			}
			data, err := os.ReadFile(ledger.path)
			if err != nil {
				t.Fatalf("read saved ledger: %v", err)
			}
			if !bytes.Contains(data, []byte(`"version":4`)) ||
				!bytes.Contains(data, []byte(`"state":"unknown"`)) {
				t.Fatalf("saved ledger lost version 4 evidence: %s", data)
			}
			got, err := ledger.Load()
			if err != nil {
				t.Fatalf("Load saved ledger: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip mismatch\ngot:  %#v\nwant: %#v", got, want)
			}
		})
	}
}

func TestLedgerOlderVersionsRejectV4Evidence(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		for _, nested := range []bool{false, true} {
			t.Run(fmt.Sprintf("v%d/nested=%t", version, nested), func(t *testing.T) {
				ledger := newTestLedgerStore(t)
				record := validTestLedger(version)
				if nested {
					retired := validTestRetired(0)
					retired.Record.UI.State = "unknown"
					record.Retired = []engine.LedgerRetired{retired}
				} else {
					record.UI = validTestLedgerUI()
					record.UI.State = "unknown"
				}
				writeLedgerRecord(t, ledger, record)
				if _, err := ledger.Load(); err == nil {
					t.Fatal("older ledger accepted version 4 evidence")
				}
			})
		}
	}
}

func TestLedgerV3AndV4Validation(t *testing.T) {
	cases := []struct {
		name     string
		evidence string
		wantErr  bool
	}{
		{"null ui", `"ui":null`, false},
		{"null nested ui", `"retired":[{"record":{"ui":null},"budget_issued":false,"attempts":0}]`, false},
		{"invalid ui state", `"ui":{"operation_id":"Pinput","origin":"Pstart","epoch":1,"step":1,"kind":"tap","display_id":12,"display_generation":"display-generation","device_id":"device-a","boot_id":"` + testLedgerBootID + `","state":"finished"}`, true},
		{"null boolean", `"legacy_ui_pending":null`, true},
		{"invalid retired attempts", `"retired":[{"record":{},"budget_issued":false,"attempts":4}]`, true},
		{"null retired", `"retired":null`, true},
		{"missing retired budget", `"retired":[{"record":{},"attempts":0}]`, true},
		{"null nested boolean", `"retired":[{"record":{"legacy_ui_pending":null},"budget_issued":false,"attempts":0}]`, true},
		{"null session identity", `"session":{"device_id":null}`, true},
	}
	for _, version := range []int{3, 4} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("v%d/%s", version, tc.name), func(t *testing.T) {
				ledger := newTestLedgerStore(t)
				body := fmt.Sprintf(`{"format":"mobile-use-vnext-ledger","version":%d,"revision":0,%s}`, version, tc.evidence)
				writeLedgerBytes(t, ledger, []byte(body))
				if _, err := ledger.Load(); (err != nil) != tc.wantErr {
					t.Fatalf("Load error = %v, want error = %t", err, tc.wantErr)
				}
			})
		}
	}
}

func TestHasVersionFourEvidence(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"top-level unknown", `{"ui":{"state": "unknown" }}`, true},
		{"nested unknown", `{"retired":[{"record":{}},{"record":{"ui":{"state":"unknown"}}}]}`, true},
		{"no ui", `{}`, false},
		{"null ui", `{"ui":null}`, false},
		{"null nested ui", `{"retired":[{"record":{"ui":null}}]}`, false},
		{"null retired", `{"retired":null}`, false},
		{"known state", `{"ui":{"state":"terminated"}}`, false},
		{"different case", `{"ui":{"state":"Unknown"}}`, false},
		{"malformed ui", `{"ui":[]}`, true},
		{"malformed nested record", `{"retired":[{"record":[]}]}`, true},
		{"malformed json", `{`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasVersionFourEvidence([]byte(tc.body)); got != tc.want {
				t.Fatalf("hasVersionFourEvidence = %t, want %t", got, tc.want)
			}
		})
	}
}
