package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
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

func TestLegacyUIRecoveryRetiresWithoutRebootGuidance(t *testing.T) {
	record := uiRecoveryTestLedger(1, nil)
	originalSession := copyUIRecoveryLedgerForTest(record).Session
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	syncRecovery(t, e, readySync())
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertCanStart(t, e)
	retired := retiredLedgerForOrigin(t, e, "Pold")
	if !reflect.DeepEqual(retired.Record.Session, &LedgerSession{
		Origin: "Pold", Agent: "agent", Mode: "virtual", State: SessionEnded,
		AppInstanceID: testAppID, ModeGeneration: testModeID,
		DisplayID: 12, DisplayGeneration: testDisplayGen,
		Width: 1080, Height: 2400, Density: 420,
		StartedAt: originalSession.StartedAt,
	}) {
		t.Fatalf("legacy migration rewrote the original session: %+v", retired.Record.Session)
	}
	if retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.ExecReason != "ui_input" ||
		retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed {
		t.Fatalf("legacy UI uncertainty was not preserved: %+v", retired.Record.Cleanup)
	}
	if retired.Record.LegacyUI != nil {
		t.Fatalf("legacy migration invented checkpoint evidence: %+v", retired.Record.LegacyUI)
	}
	saved := store.lastSave(t)
	if saved.Version != ledgerVersion || saved.Session != nil || saved.Cleanup != nil ||
		saved.UI != nil || saved.LegacyUI != nil || len(saved.Retired) != 1 {
		t.Fatalf("v1 migration was not written as top-level-clean v3: %+v", saved)
	}
	e.mu.Lock()
	operationCount := len(e.operations)
	_, oldOperationPreserved := e.operations["Pold"]
	e.mu.Unlock()
	if operationCount != 1 || !oldOperationPreserved {
		t.Fatalf("legacy migration invented or rewrote operation routes: %d", operationCount)
	}
}

func TestMigratedLegacyUIRetiredSurvivesRestartWithoutRebudget(t *testing.T) {
	record := uiRecoveryTestLedger(1, nil)
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	syncRecovery(t, e, readySync())
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	first := store.lastSave(t)
	if first.Version != ledgerVersion || len(first.Retired) != 1 ||
		!first.Retired[0].BudgetIssued {
		t.Fatalf("v1 UI migration was not durably retired: %+v", first)
	}
	firstAttempts := first.Retired[0].Attempts

	restarted, restartedStore := newUIRecoveryTestEngine(copyUIRecoveryLedgerForTest(first), newFakeDevice())
	syncRecovery(t, restarted, readySync())
	waitRecoveryAttempt(t, restarted)
	waitRetiredCleanupStopped(t, restarted, "Pold")
	assertCanStart(t, restarted)
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for restartedStore.saveCount() == 0 {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-deadline.C:
			t.Fatal("restart retirement snapshot was not saved")
		}
	}
	second := restartedStore.lastSave(t)
	if second.Version != ledgerVersion || len(second.Retired) != 1 {
		t.Fatalf("restart lost the v3 retired record: %+v", second)
	}
	retired := second.Retired[0]
	if !retired.BudgetIssued || retired.Attempts != firstAttempts ||
		retired.StoppedReason != "cleanup_restart_stopped" {
		t.Fatalf("restart renewed or reset the retired budget: %+v", retired)
	}
	if retired.Record == nil || retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed {
		t.Fatalf("restart fabricated a confirmed UI end: %+v", retired.Record)
	}
}

func TestLegacyUIRecoveryPersistsRetiredBeforeCompensation(t *testing.T) {
	record := uiRecoveryTestLedger(1, nil)
	var deviceIO atomic.Bool
	base := newFakeDevice()
	dev := &hookedDevice{
		Device: base,
		probe: func(ctx context.Context, id int) (device.DisplayState, error) {
			deviceIO.Store(true)
			return base.ProbeDisplay(ctx, id)
		},
		tasks: func(ctx context.Context) ([]device.Task, error) {
			deviceIO.Store(true)
			return base.TaskList(ctx)
		},
	}
	setUIRecoveryIdentity(base, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	store.setSaveHook(func(ctx context.Context, candidate *Ledger) error {
		if len(candidate.Retired) == 0 {
			return nil
		}
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
		return nil
	})
	done := make(chan struct{})
	go func() {
		syncRecovery(t, e, readySync())
		close(done)
	}()
	awaitSignal(t, entered)
	saved := store.lastSave(t)
	if saved.Version != ledgerVersion || saved.Session != nil ||
		saved.Cleanup != nil || len(saved.Retired) != 1 ||
		saved.Retired[0].Record == nil || saved.Retired[0].Record.Cleanup == nil ||
		saved.Retired[0].Record.Cleanup.ExecReason != "ui_input" {
		t.Fatalf("retirement was not durably captured before compensation: %+v", saved)
	}
	if deviceIO.Load() {
		t.Fatal("physical cleanup started before the retired snapshot was saved")
	}
	close(release)
	awaitSignal(t, done)
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
}

func TestLegacyUIRecoverySaveFailureIsCurrentCapabilityGate(t *testing.T) {
	record := uiRecoveryTestLedger(1, nil)
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)
	store.setSaveHook(func(context.Context, *Ledger) error {
		return errors.New("synthetic ledger failure")
	})

	syncRecovery(t, e, readySync())
	waitRecoveryAttempt(t, e)
	status, apiErr := e.Status("agent", "")
	if apiErr != nil || status.CanStart == nil || *status.CanStart ||
		status.BlockingCode != "ledger_unavailable" {
		t.Fatalf("failed retirement save did not remain a current ledger gate: %+v %v", status, apiErr)
	}
	if status.Recovery != nil && status.Recovery.Entry == protocol.RecoveryEntryUser {
		t.Fatalf("failed retirement save requested an unnecessary reboot: %+v", status.Recovery)
	}
	retired := retiredLedgerForOrigin(t, e, "Pold")
	if retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed {
		t.Fatalf("failed save fabricated confirmed execution: %+v", retired.Record.Cleanup)
	}

	store.setSaveHook(nil)
	if err := e.persistSync(context.Background()); err != nil {
		t.Fatalf("retry after save recovery: %v", err)
	}
	assertCanStart(t, e)
}

func TestV2UIRecoverySameBootRetiresWithoutRebootGate(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("may_dispatch"))
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	syncRecovery(t, e, readySync())
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertCanStart(t, e)
	retired := retiredLedgerForOrigin(t, e, "Pold")
	if retired.Record.UI == nil || retired.Record.UI.State != "may_dispatch" ||
		retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed {
		t.Fatalf("same-boot unknown UI was erased or confirmed: %+v", retired.Record)
	}
	saved := store.lastSave(t)
	if saved.Session != nil || saved.UI != nil || len(saved.Retired) != 1 {
		t.Fatalf("same-boot UI remained in v3 current fields: %+v", saved)
	}
}

func TestV2UIRecoveryCrossBootPromotesOnlyExecution(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("may_dispatch"))
	base := newFakeDevice()
	setUIRecoveryIdentity(base, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootB}, nil)
	base.mu.Lock()
	base.displayExists = false
	base.mu.Unlock()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseIO := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseIO()
	var physicalRead sync.Once
	dev := &hookedDevice{
		Device: base,
		tasks: func(ctx context.Context) ([]device.Task, error) {
			physicalRead.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return base.TaskList(ctx)
		},
	}
	e, store := newUIRecoveryTestEngine(record, dev)

	syncRecovery(t, e, readySync())
	awaitSignal(t, entered)
	retired := retiredLedgerForOrigin(t, e, "Pold")
	if retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.Execution != protocol.CleanupConfirmed ||
		!retired.Record.Cleanup.ExecutionRecheckRequired {
		t.Errorf("cross-boot promotion lost its physical recheck before device I/O: %+v", retired.Record.Cleanup)
	}
	if retired.Record.UI == nil || retired.Record.UI.State != "may_dispatch" {
		t.Errorf("cross boot rewrote the original UI token: %+v", retired.Record.UI)
	}
	if t.Failed() {
		return
	}
	saved := store.lastSave(t)
	if saved.Version != ledgerVersion || saved.Session != nil ||
		saved.UI != nil || saved.Cleanup != nil || len(saved.Retired) != 1 {
		t.Fatalf("pre-recheck retirement was not written as v3 retired ownership: %+v", saved)
	}
	releaseIO()
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertCanStart(t, e)
	retired = retiredLedgerForOrigin(t, e, "Pold")
	if retired.Record.Cleanup == nil || retired.Record.Cleanup.Execution != protocol.CleanupConfirmed ||
		retired.Record.Cleanup.Recents != protocol.CleanupUnconfirmed ||
		retired.Record.Cleanup.State == protocol.CleanupComplete {
		t.Fatalf("cross-boot recheck trusted stale or unreadable physical recents: %+v", retired.Record.Cleanup)
	}
	if retired.Record.UI == nil || retired.Record.UI.State != "may_dispatch" {
		t.Fatalf("cross boot rewrote the original UI token: %+v", retired.Record.UI)
	}
	base.mu.Lock()
	removals := append([]int(nil), base.removeCalls...)
	base.mu.Unlock()
	if len(removals) != 0 {
		t.Fatalf("cross-boot recovery dispatched a destructive task removal: %v", removals)
	}
}

func TestTerminalUIDoesNotFabricateLegacyCheckpointRelease(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("terminated"))
	record.LegacyUI = &LedgerUICheckpoint{
		Origin: "Pold", DeviceID: "device-test", BootID: uiRecoveryBootA,
	}
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootA}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	syncRecovery(t, e, readySync())
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertCanStart(t, e)
	retired := retiredLedgerForOrigin(t, e, "Pold")
	if retired.Record.LegacyUI == nil ||
		*retired.Record.LegacyUI != *record.LegacyUI {
		t.Fatalf("pending legacy checkpoint was lost or rewritten: %+v", retired.Record.LegacyUI)
	}
	if retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed ||
		retired.Record.Cleanup.State == protocol.CleanupComplete {
		t.Fatalf("terminal UI fabricated release of the legacy checkpoint: %+v", retired.Record.Cleanup)
	}
	saved := store.lastSave(t)
	if saved.LegacyUI != nil || saved.Session != nil || len(saved.Retired) != 1 {
		t.Fatalf("retired legacy checkpoint remained in current fields: %+v", saved)
	}
}

func TestUIRecoveryRetiresShellInsteadOfRestoringCurrentOwnership(t *testing.T) {
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("terminated"))
	record.Shell = &LedgerShell{
		RunnerID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		State:    ShellRunning,
	}
	dev := newFakeDevice()
	setUIRecoveryIdentity(dev, device.BootIdentity{DeviceID: "device-test", BootID: uiRecoveryBootB}, nil)
	e, store := newUIRecoveryTestEngine(record, dev)

	syncRecovery(t, e, readySync())
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	saved := store.lastSave(t)
	if saved.Shell != nil || saved.Session != nil || len(saved.Retired) != 1 ||
		saved.Retired[0].Record == nil ||
		saved.Retired[0].Record.Shell == nil ||
		saved.Retired[0].Record.Shell.RunnerID != record.Shell.RunnerID {
		t.Fatalf("UI recovery did not move the shell fact into retired ownership: %+v", saved)
	}
}
