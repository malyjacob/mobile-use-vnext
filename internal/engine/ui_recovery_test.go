package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	uiRecoveryBootA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	uiRecoveryBootB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

type uiRecoveryTestStore struct {
	mu        sync.Mutex
	saves     []*Ledger
	saveHook  func(context.Context, *Ledger) error
	saveError error
}

func (s *uiRecoveryTestStore) Load() (*Ledger, error) {
	return nil, nil
}

func (s *uiRecoveryTestStore) Save(ctx context.Context, record *Ledger) (int64, error) {
	snapshot := copyUIRecoveryLedgerForTest(record)
	s.mu.Lock()
	s.saves = append(s.saves, snapshot)
	hook, saveError := s.saveHook, s.saveError
	revision := int64(len(s.saves))
	s.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, snapshot); err != nil {
			return 0, err
		}
	}
	if saveError != nil {
		return 0, saveError
	}
	return revision, nil
}

func (s *uiRecoveryTestStore) setSaveHook(hook func(context.Context, *Ledger) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveHook = hook
}

func (s *uiRecoveryTestStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.saves)
}

func (s *uiRecoveryTestStore) lastSave(t *testing.T) *Ledger {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saves) == 0 {
		t.Fatal("ledger was not saved")
	}
	return copyUIRecoveryLedgerForTest(s.saves[len(s.saves)-1])
}

func copyUIRecoveryLedgerForTest(record *Ledger) *Ledger {
	if record == nil {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		panic(err)
	}
	var snapshot Ledger
	if err := json.Unmarshal(data, &snapshot); err != nil {
		panic(err)
	}
	return &snapshot
}

func uiRecoveryTestLedger(version int, ui *LedgerUI) *Ledger {
	now := time.Date(2026, 11, 10, 4, 5, 6, 0, time.UTC)
	record := &Ledger{
		Format: ledgerFormat, Version: version, Revision: 7,
		ServiceInstanceID: "service-old", UpdatedAt: now, AppInstanceID: testAppID,
		Session: &LedgerSession{
			Origin: "Pold", Agent: "agent", Mode: "virtual", State: SessionActive,
			AppInstanceID: testAppID, ModeGeneration: testModeID,
			DisplayID: 12, DisplayGeneration: testDisplayGen,
			Width: 1080, Height: 2400, Density: 420, StartedAt: now,
		},
		Cleanup: &LedgerCleanup{
			State: protocol.CleanupUnconfirmed, Execution: protocol.CleanupUnconfirmed,
			Tasks: protocol.CleanupConfirmed, Recents: protocol.CleanupConfirmed,
			Display: protocol.CleanupConfirmed, Reason: "user_stop",
			ExecReason: "ui_input", StartedAt: now,
		},
		UI: ui,
	}
	return record
}

func uiRecoveryTestUI(state string) *LedgerUI {
	return &LedgerUI{
		OperationID: "Pinput", Origin: "Pold", Epoch: 1, Step: 2, Kind: "tap",
		DisplayID: 12, DisplayGeneration: testDisplayGen,
		DeviceID: "device-test", BootID: uiRecoveryBootA, State: state,
	}
}

func setUIRecoveryIdentity(dev *fakeDevice, identity device.BootIdentity, identityErr error) {
	dev.mu.Lock()
	defer dev.mu.Unlock()
	dev.deviceID, dev.bootID, dev.identityErr = identity.DeviceID, identity.BootID, identityErr
}

func newUIRecoveryTestEngine(record *Ledger, dev Device) (*Engine, *uiRecoveryTestStore) {
	store := &uiRecoveryTestStore{}
	cfg := DefaultConfig("new", "agent")
	cfg.LedgerTimeout = time.Second
	e := New(cfg, nil, dev, store, nil, nil)
	e.SetDeviceReady(true)
	e.mu.Lock()
	e.recovering = true
	e.recovery = record
	e.restoreUIRecoveryLocked(record)
	e.mu.Unlock()
	return e, store
}

func TestRestoreUIRecoveryDeepCopiesEvidence(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("may_dispatch"))
	record.LegacyUI = &LedgerUICheckpoint{
		Origin: "Pold", DeviceID: "device-test", BootID: uiRecoveryBootA,
	}
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, _ := newUIRecoveryTestEngine(record, dev)

	record.UI.State = "terminated"
	record.LegacyUI.BootID = uiRecoveryBootB

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ui == nil || e.ui.record.State != "may_dispatch" || !e.ui.durable {
		t.Fatalf("restored UI shares caller state or lost durability: %+v", e.ui)
	}
	if e.legacyUI == nil || e.legacyUI.BootID != uiRecoveryBootA || !e.uiCheckpointReady {
		t.Fatalf("restored checkpoint shares caller state: %+v ready=%v", e.legacyUI, e.uiCheckpointReady)
	}
}

func TestLegacyUICheckpointPersistsBeforeRebootGuidance(t *testing.T) {
	record := uiRecoveryTestLedger(1, nil)
	originalSession := copyUIRecoveryLedgerForTest(record).Session
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
	if resolved || bootChanged || reason != "ui_input_reboot_required" {
		t.Fatalf("first legacy resolution=(%v,%v,%q), want retained after durable checkpoint", resolved, bootChanged, reason)
	}
	if store.saveCount() != 1 {
		t.Fatalf("checkpoint save count=%d, want 1", store.saveCount())
	}
	first := store.lastSave(t)
	if first.Version != 2 || first.LegacyUI == nil ||
		first.LegacyUI.Origin != "Pold" || first.LegacyUI.DeviceID != "device-test" ||
		first.LegacyUI.BootID != uiRecoveryBootA {
		t.Fatalf("legacy checkpoint was not durably projected: %+v", first)
	}
	e.mu.Lock()
	ready := e.uiCheckpointReady
	e.mu.Unlock()
	if !ready {
		t.Fatal("checkpoint was saved but remained non-durable in engine state")
	}

	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootB}, nil)
	resolved, bootChanged, reason = e.resolveRecoveryUI(context.Background(), record)
	if !resolved || !bootChanged || reason != "" {
		t.Fatalf("cross-boot legacy resolution=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	if store.saveCount() != 2 {
		t.Fatalf("execution promotion save count=%d, want 2", store.saveCount())
	}
	second := store.lastSave(t)
	if second.Cleanup == nil || second.Cleanup.Execution != protocol.CleanupConfirmed ||
		second.Cleanup.State != protocol.CleanupUnconfirmed {
		t.Fatalf("cross boot did not promote only execution: %+v", second.Cleanup)
	}
	if !reflect.DeepEqual(second.Session, originalSession) {
		t.Fatalf("cross boot rewrote old session responsibility: %+v", second.Session)
	}
	if second.LegacyUI == nil || *second.LegacyUI != *first.LegacyUI {
		t.Fatalf("checkpoint changed after retry: first=%+v second=%+v", first.LegacyUI, second.LegacyUI)
	}
	e.mu.Lock()
	operationCount := len(e.operations)
	e.mu.Unlock()
	if operationCount != 0 {
		t.Fatalf("cross boot invented or rewrote P records: %d", operationCount)
	}
}

func TestLegacyUIPendingSurvivesSameBootRetryAndRestart(t *testing.T) {
	record := uiRecoveryTestLedger(1, nil)
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
	if resolved || bootChanged || reason != "ui_input_reboot_required" {
		t.Fatalf("first legacy resolution=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	first := store.lastSave(t)
	if !first.LegacyUIPending {
		t.Fatal("checkpoint save lost the version 2 legacy migration marker")
	}
	if !record.LegacyUIPending {
		t.Fatal("committed recovery record lost the version 2 legacy migration marker")
	}

	resolved, bootChanged, reason = e.resolveRecoveryUI(context.Background(), record)
	if resolved || bootChanged || reason != "ui_input_reboot_required" {
		t.Fatalf("same-boot retry=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	if !record.LegacyUIPending || store.saveCount() != 1 {
		t.Fatal("same-boot retry dropped the marker or rewrote the immutable checkpoint")
	}

	restartedRecord := copyUIRecoveryLedgerForTest(first)
	restartedDevice := newFakeDevice()
	setUIRecoveryIdentity(restartedDevice, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	restarted, restartedStore := newUIRecoveryTestEngine(restartedRecord, restartedDevice)
	resolved, bootChanged, reason = restarted.resolveRecoveryUI(context.Background(), restartedRecord)
	if resolved || bootChanged || reason != "ui_input_reboot_required" {
		t.Fatalf("restart same-boot resolution=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	if !restartedRecord.LegacyUIPending || restartedStore.saveCount() != 0 {
		t.Fatal("restart lost the migration marker or unexpectedly rewrote the checkpoint")
	}
}

func TestLegacyUIPendingSnapshotSurvivesRestartUntilCheckpoint(t *testing.T) {
	record := uiRecoveryTestLedger(1, nil)
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, _ := newUIRecoveryTestEngine(record, dev)
	e.mu.Lock()
	snapshot := copyUIRecoveryLedgerForTest(e.ledgerRecordLocked())
	e.mu.Unlock()
	snapshot.Version = 2
	if !snapshot.LegacyUIPending || snapshot.LegacyUI != nil {
		t.Fatalf("pre-checkpoint sync snapshot lost the migration need: %+v", snapshot)
	}

	restartedDevice := newFakeDevice()
	setUIRecoveryIdentity(restartedDevice, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	restarted, store := newUIRecoveryTestEngine(snapshot, restartedDevice)
	resolved, bootChanged, reason := restarted.resolveRecoveryUI(context.Background(), snapshot)
	if resolved || bootChanged || reason != "ui_input_reboot_required" {
		t.Fatalf("v2 pending snapshot resolution=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	if !snapshot.LegacyUIPending || snapshot.LegacyUI == nil {
		t.Fatal("restart resolution did not retain the marker and establish the checkpoint")
	}
	saved := store.lastSave(t)
	if !saved.LegacyUIPending || saved.LegacyUI == nil {
		t.Fatalf("checkpoint save dropped restart migration evidence: %+v", saved)
	}

	resolved, bootChanged, reason = restarted.resolveRecoveryUI(context.Background(), snapshot)
	if resolved || bootChanged || reason != "ui_input_reboot_required" {
		t.Fatalf("post-checkpoint same-boot retry=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	if !snapshot.LegacyUIPending || store.saveCount() != 1 {
		t.Fatal("same-boot retry dropped the marker or rewrote the checkpoint")
	}
}

func TestLegacyUIPendingUsesOriginalSessionAndCleanupFacts(t *testing.T) {
	record := uiRecoveryTestLedger(2, nil)
	record.LegacyUIPending = true

	origin, pending := legacyUIResponsibility(record)
	if origin != "Pold" || !pending {
		t.Fatalf("v2 legacy marker was ignored: origin=%q pending=%v", origin, pending)
	}

	record.Session.State = SessionEnded
	record.Cleanup.Execution = protocol.CleanupConfirmed
	if origin, pending := legacyUIResponsibility(record); origin != "" || pending {
		t.Fatalf("completed original facts remained pending: origin=%q pending=%v", origin, pending)
	}
}

func TestLegacyUICheckpointSaveFailureNeverClaimsRebootGuidance(t *testing.T) {
	record := uiRecoveryTestLedger(1, nil)
	before := copyUIRecoveryLedgerForTest(record)
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)
	store.setSaveHook(func(context.Context, *Ledger) error {
		return errors.New("synthetic ledger failure")
	})

	resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
	if resolved || bootChanged || reason != "ledger_unavailable" {
		t.Fatalf("failed checkpoint save=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	if !reflect.DeepEqual(record, before) {
		t.Fatalf("failed candidate changed public recovery state: %+v", record)
	}
	e.mu.Lock()
	ready, pending := e.uiCheckpointReady, e.legacyUI
	e.mu.Unlock()
	if ready || pending == nil {
		t.Fatalf("failed save checkpoint state ready=%v pending=%+v", ready, pending)
	}
	pendingBefore := *pending

	store.setSaveHook(nil)
	resolved, bootChanged, reason = e.resolveRecoveryUI(context.Background(), record)
	if resolved || bootChanged || reason != "ui_input_reboot_required" {
		t.Fatalf("retry after save recovery=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	saved := store.lastSave(t)
	if saved.LegacyUI == nil || *saved.LegacyUI != pendingBefore {
		t.Fatalf("retry replaced immutable pending checkpoint: saved=%+v pending=%+v", saved.LegacyUI, pendingBefore)
	}
}

func TestUIRecoveryUnsafeIdentityRetainsResponsibility(t *testing.T) {
	cases := []struct {
		name     string
		identity device.BootIdentity
		err      error
	}{
		{name: "different device", identity: device.BootIdentity{DeviceID: "device-other", BootID: uiRecoveryBootA}},
		{name: "invalid boot", identity: device.BootIdentity{DeviceID: "device-test", BootID: "boot-invalid"}},
		{name: "read failure", identity: device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, err: errors.New("identity unavailable")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := uiRecoveryTestLedger(2, uiRecoveryTestUI("may_dispatch"))
			before := copyUIRecoveryLedgerForTest(record)
			dev := newFakeDevice()
			setUIRecoveryIdentity(dev, tc.identity, tc.err)
			e, store := newUIRecoveryTestEngine(record, dev)

			resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
			if resolved || bootChanged || reason != "ui_input_unconfirmed" {
				t.Fatalf("unsafe identity=(%v,%v,%q), want retained", resolved, bootChanged, reason)
			}
			if store.saveCount() != 0 || !reflect.DeepEqual(record, before) {
				t.Fatal("unsafe identity changed or saved recovery state")
			}
		})
	}
}

func TestV2UIRecoverySameBootRequiresReboot(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("may_dispatch"))
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
	if resolved || bootChanged || reason != "ui_input_reboot_required" {
		t.Fatalf("same-boot UI resolution=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	if store.saveCount() != 0 {
		t.Fatal("same-boot durable UI state caused a redundant save before recovery")
	}
}

func TestV2UIRecoveryCrossBootPromotesOnlyExecution(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("may_dispatch"))
	before := copyUIRecoveryLedgerForTest(record)
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootB}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
	if !resolved || !bootChanged || reason != "" {
		t.Fatalf("cross-boot UI resolution=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	if store.saveCount() != 1 {
		t.Fatalf("cross-boot promotion save count=%d, want 1", store.saveCount())
	}
	saved := store.lastSave(t)
	if saved.Cleanup == nil || saved.Cleanup.Execution != protocol.CleanupConfirmed {
		t.Fatalf("execution was not promoted: %+v", saved.Cleanup)
	}
	wantCleanup := *before.Cleanup
	wantCleanup.Execution = protocol.CleanupConfirmed
	if !reflect.DeepEqual(saved.Cleanup, &wantCleanup) {
		t.Fatalf("cross boot changed cleanup beyond execution:\ngot  %+v\nwant %+v", saved.Cleanup, &wantCleanup)
	}
	if !reflect.DeepEqual(saved.Session, before.Session) ||
		!reflect.DeepEqual(saved.UI, before.UI) ||
		saved.Version != 2 {
		t.Fatalf("cross boot rewrote unrelated responsibility: %+v", saved)
	}
}

func TestTerminalUIEvidencePromotesExecutionAndReportsCrossBootBoundary(t *testing.T) {
	cases := []struct {
		state       string
		name        string
		boot        string
		bootChanged bool
	}{
		{state: "terminated", name: "terminated same boot", boot: uiRecoveryBootA},
		{state: "terminated", name: "terminated different boot", boot: uiRecoveryBootB, bootChanged: true},
		{state: "not_dispatched", name: "not dispatched same boot", boot: uiRecoveryBootA},
		{state: "not_dispatched", name: "not dispatched different boot", boot: uiRecoveryBootB, bootChanged: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := uiRecoveryTestLedger(2, uiRecoveryTestUI(tc.state))
			dev := newFakeDevice()
			setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: tc.boot}, nil)
			e, store := newUIRecoveryTestEngine(record, dev)

			resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
			if !resolved || bootChanged != tc.bootChanged || reason != "" {
				t.Fatalf("terminal UI resolution=(%v,%v,%q), want bootChanged=%v", resolved, bootChanged, reason, tc.bootChanged)
			}
			if store.saveCount() != 1 {
				t.Fatalf("terminal UI receipt promotion saves=%d, want 1", store.saveCount())
			}
			saved := store.lastSave(t)
			if saved.Cleanup == nil || saved.Cleanup.Execution != protocol.CleanupConfirmed {
				t.Fatalf("terminal UI receipt did not promote execution: %+v", saved.Cleanup)
			}
		})
	}
}

func TestTerminalUIDoesNotPromoteShellExecution(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("terminated"))
	record.Cleanup.ExecReason = "shell_chain"
	before := copyUIRecoveryLedgerForTest(record)
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootB}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
	if !resolved || !bootChanged || reason != "" {
		t.Fatalf("terminal UI resolution=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	if store.saveCount() != 0 || !reflect.DeepEqual(record.Cleanup, before.Cleanup) {
		t.Fatal("terminal UI receipt cleared the independent shell execution responsibility")
	}
}

func TestTerminalUIDoesNotReleasePendingLegacyCheckpoint(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("terminated"))
	record.LegacyUI = &LedgerUICheckpoint{
		Origin: "Pold", DeviceID: "device-test", BootID: uiRecoveryBootA,
	}
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
	if resolved || bootChanged || reason != "ui_input_reboot_required" {
		t.Fatalf("terminal UI released pending legacy checkpoint: (%v,%v,%q)", resolved, bootChanged, reason)
	}
	if store.saveCount() != 0 || record.Cleanup.Execution != protocol.CleanupUnconfirmed {
		t.Fatal("pending legacy checkpoint did not retain execution responsibility")
	}
}

func TestUIRecoverySnapshotDoesNotRestoreResolvedShell(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("terminated"))
	record.Shell = &LedgerShell{
		RunnerID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		State:    ShellRunning,
	}
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootB}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)
	e.mu.Lock()
	e.shell = nil
	e.mu.Unlock()

	resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
	if !resolved || !bootChanged || reason != "" {
		t.Fatalf("terminal UI resolution=(%v,%v,%q)", resolved, bootChanged, reason)
	}
	saved := store.lastSave(t)
	if saved.Shell != nil {
		t.Fatalf("UI recovery save resurrected a resolved shell chain: %+v", saved.Shell)
	}
}

func TestUIRecoveryWithoutResponsibilityDoesNotReadIdentity(t *testing.T) {
	record := uiRecoveryTestLedger(2, nil)
	record.Session.State = SessionEnded
	record.Cleanup = nil
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{}, errors.New("identity must not be read"))
	e, _ := newUIRecoveryTestEngine(record, dev)

	resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
	if !resolved || bootChanged || reason != "" {
		t.Fatalf("plain record resolution=(%v,%v,%q)", resolved, bootChanged, reason)
	}
}

type uiRecoveryIdentityHookDevice struct {
	Device
	identity func(context.Context) (device.BootIdentity, error)
}

func (d *uiRecoveryIdentityHookDevice) ExecutionIdentity(ctx context.Context) (device.BootIdentity, error) {
	return d.identity(ctx)
}

func TestUIRecoveryRevalidatesOwnershipAfterIdentityRead(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("may_dispatch"))
	base := newFakeDevice()
	setUIRecoveryIdentity(base, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	dev := &uiRecoveryIdentityHookDevice{
		Device: base,
		identity: func(context.Context) (device.BootIdentity, error) {
			close(entered)
			<-release
			return device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil
		},
	}
	e, store := newUIRecoveryTestEngine(record, dev)
	type outcome struct {
		resolved    bool
		bootChanged bool
		reason      string
	}
	done := make(chan outcome, 1)
	go func() {
		resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
		done <- outcome{resolved: resolved, bootChanged: bootChanged, reason: reason}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("identity read was not reached")
	}
	e.mu.Lock()
	e.recovery = nil
	e.mu.Unlock()
	close(release)
	got := <-done
	if got.resolved || got.bootChanged || got.reason != "recovery_pending" {
		t.Fatalf("stale identity result=(%+v)", got)
	}
	if store.saveCount() != 0 {
		t.Fatal("stale identity result was saved")
	}
}

func TestUIRecoveryRevalidatesOwnershipAfterSave(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("may_dispatch"))
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootB}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)
	entered, release := make(chan struct{}), make(chan struct{})
	store.setSaveHook(func(context.Context, *Ledger) error {
		close(entered)
		<-release
		return nil
	})
	type outcome struct {
		resolved    bool
		bootChanged bool
		reason      string
	}
	done := make(chan outcome, 1)
	go func() {
		resolved, bootChanged, reason := e.resolveRecoveryUI(context.Background(), record)
		done <- outcome{resolved: resolved, bootChanged: bootChanged, reason: reason}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Save was not reached")
	}
	e.mu.Lock()
	e.shuttingDown = true
	e.mu.Unlock()
	close(release)
	got := <-done
	if got.resolved || got.bootChanged || got.reason != "recovery_pending" {
		t.Fatalf("post-save stale result=(%+v)", got)
	}
	if record.Cleanup.Execution != protocol.CleanupUnconfirmed {
		t.Fatal("post-save ownership loss still committed the promotion")
	}
}
