package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const testAppID = "11111111-1111-4111-8111-111111111111"
const testModeID = "22222222-2222-4222-8222-222222222222"
const testDisplayGen = "33333333-3333-4333-8333-333333333333"

func readySync() *protocol.AppSyncRequest {
	yes, no := true, false
	return &protocol.AppSyncRequest{ContractVersion: protocol.ContractVersion, AppInstanceID: testAppID, AppVersion: "test", SelectedMode: "virtual", ModeGeneration: testModeID, ModeReady: true, StopReady: true, NotificationsEnabled: true, BackgroundRunning: true, PowerInteractive: &yes, KeyguardLocked: &no, PrimaryDisplay: &protocol.AppDisplay{DisplayID: 0, Width: 1080, Height: 2400, Density: 420, Generation: testModeID}}
}
func oldLedger() *Ledger {
	return &Ledger{Format: ledgerFormat, Version: ledgerVersion, ServiceInstanceID: "old", UpdatedAt: time.Now().Add(-time.Minute), AppInstanceID: testAppID, Session: &LedgerSession{Origin: "Pold", Agent: "agent", Mode: "virtual", State: SessionActive, AppInstanceID: testAppID, ModeGeneration: testModeID, DisplayID: 12, DisplayGeneration: testDisplayGen, Width: 1080, Height: 2400, Density: 420, StartedAt: time.Now().Add(-time.Minute)}}
}
func recoveryEngine(t *testing.T, record *Ledger, dev Device) *Engine {
	t.Helper()
	cfg := DefaultConfig("new", "agent")
	cfg.CleanupTimeout = 200 * time.Millisecond
	cfg.LedgerTimeout = 200 * time.Millisecond
	e := New(cfg, nil, dev, &fakeStore{record: record}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	e.SetDeviceReady(true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		e.Shutdown(ctx)
	})
	return e
}
func assertCannotStart(t *testing.T, e *Engine) {
	t.Helper()
	r, err := e.Status("agent", "")
	if err != nil || r.CanStart == nil || *r.CanStart {
		t.Fatalf("recovery admitted start: %+v %v", r, err)
	}
}
func syncRecovery(t *testing.T, e *Engine, req *protocol.AppSyncRequest) *protocol.AppSyncResponse {
	t.Helper()
	r, err := e.SyncApp(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func waitRecoveryAttempt(t *testing.T, e *Engine) {
	t.Helper()
	// RetryRecovery is synchronous; SyncApp may already own the recovery pass.
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		e.RetryRecovery(context.Background())
		e.mu.Lock()
		recovering := e.recovery != nil
		reason := e.recoveryReason
		change := e.appNotify
		e.mu.Unlock()
		if !recovering || reason != "" {
			return
		}
		select {
		case <-change:
		case <-deadline.C:
			t.Fatal("recovery did not settle")
		}
	}
}

func TestRecoveryDoesNotForgetAbsentDisplayBeforeAppSync(t *testing.T) {
	dev := newFakeDevice()
	dev.displayExists = false
	e := recoveryEngine(t, oldLedger(), dev)
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCannotStart(t, e)
	e.mu.Lock()
	retained := e.recovery != nil
	e.mu.Unlock()
	if !retained {
		t.Fatal("display absence discarded old ownership before paired App sync")
	}
}

func TestRecoveryRequiresSuccessfulTaskReadDespiteAbsentDisplay(t *testing.T) {
	for _, tasks := range []bool{false, true} {
		name := "read_failed"
		if tasks {
			name = "old_task_present"
		}
		t.Run(name, func(t *testing.T) {
			base := newFakeDevice()
			base.displayExists = false
			dev := &hookedDevice{Device: base, tasks: func(context.Context) ([]device.Task, error) {
				if tasks {
					return []device.Task{{TaskID: 42, DisplayID: 12}}, nil
				}
				return nil, errors.New("unavailable")
			}}
			e := recoveryEngine(t, oldLedger(), dev)
			if err := e.StartRecovery(context.Background()); err != nil {
				t.Fatal(err)
			}
			syncRecovery(t, e, readySync())
			waitRecoveryAttempt(t, e)
			assertCannotStart(t, e)
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.recovery == nil {
				t.Error("lost unresolved task ownership")
			}
		})
	}
}

func TestRecoveryPreservesUnconfirmedExecutionAndRecents(t *testing.T) {
	for _, field := range []string{"execution", "recents"} {
		t.Run(field, func(t *testing.T) {
			record := oldLedger()
			record.Cleanup = &LedgerCleanup{State: protocol.CleanupUnconfirmed, Execution: protocol.CleanupNotApplicable, Tasks: protocol.CleanupConfirmed, Recents: protocol.CleanupNotApplicable, Display: protocol.CleanupUnconfirmed}
			if field == "execution" {
				record.Cleanup.Execution = protocol.CleanupUnconfirmed
			} else {
				record.Cleanup.Recents = protocol.CleanupUnconfirmed
			}
			dev := newFakeDevice()
			dev.displayExists = false
			e := recoveryEngine(t, record, dev)
			if err := e.StartRecovery(context.Background()); err != nil {
				t.Fatal(err)
			}
			syncRecovery(t, e, readySync())
			waitRecoveryAttempt(t, e)
			assertCannotStart(t, e)
		})
	}
}

func TestRecoveryPendingCreateCannotExpireAwayReportedDisplay(t *testing.T) {
	record := oldLedger()
	record.Session.DisplayID = 0
	record.PendingCreate = &LedgerCommand{ID: "old-create", Kind: "create_display", AppInstanceID: testAppID, RequestedAt: record.UpdatedAt}
	e := recoveryEngine(t, record, newFakeDevice())
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	req := readySync()
	req.Display = &protocol.AppDisplay{DisplayID: 12, Width: 1080, Height: 2400, Density: 420, Generation: testDisplayGen}
	syncRecovery(t, e, req)
	waitRecoveryAttempt(t, e)
	assertCannotStart(t, e)
	r := syncRecovery(t, e, req)
	if len(r.Commands) != 1 || r.Commands[0].Kind != "close_display" || r.Commands[0].TargetAppInstanceID != testAppID || r.Commands[0].TargetDisplayID == nil || *r.Commands[0].TargetDisplayID != 12 || r.Commands[0].TargetGeneration != testDisplayGen {
		t.Fatalf("precise recovery close was not delivered: %+v", r.Commands)
	}
	if r.Control.Availability.State != AvailabilityRecovering {
		t.Fatalf("close not delivered under recovering: %+v", r.Control)
	}
}

func TestRecoveryLedgerSnapshotsKeepOldOwnershipUntilResolved(t *testing.T) {
	e := recoveryEngine(t, oldLedger(), newFakeDevice())
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	record := e.ledgerRecordLocked()
	e.mu.Unlock()
	if record.Session == nil || record.Session.Origin != "Pold" || record.Session.DisplayID != 12 {
		t.Fatalf("sync snapshot erased unresolved ledger: %+v", record)
	}
}

// waitRecoveryComplete waits until the durable record is actually resolved, not
// merely until a recovery reason settles.
func waitRecoveryComplete(t *testing.T, e *Engine) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.RetryRecovery(context.Background())
		e.mu.Lock()
		done := e.recovery == nil
		change := e.appNotify
		e.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("recovery did not resolve")
		}
		select {
		case <-change:
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestRecoveryKeepsOldOwnershipUntilResolvedThenClears inspects ledger snapshots
// taken while recovery is still pending: the old session, pending create and
// cleanup facts must survive verbatim. Only a completed recovery may drop the
// old session/pending ownership and replace cleanup with the resolved record.
func TestRecoveryKeepsOldOwnershipUntilResolvedThenClears(t *testing.T) {
	record := oldLedger()
	record.PendingCreate = &LedgerCommand{ID: "old-create", Kind: "create_display",
		AppInstanceID: testAppID, RequestedAt: record.UpdatedAt}
	record.Cleanup = &LedgerCleanup{State: protocol.CleanupPending,
		Execution: protocol.CleanupConfirmed, Tasks: protocol.CleanupConfirmed,
		Recents: protocol.CleanupNotApplicable, Display: protocol.CleanupPending}
	dev := newFakeDevice()
	dev.displayExists = false
	e := recoveryEngine(t, record, dev)
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCannotStart(t, e)
	e.mu.Lock()
	during := e.ledgerRecordLocked()
	e.mu.Unlock()
	if during.Session == nil || during.Session.Origin != "Pold" || during.Session.DisplayID != 12 {
		t.Fatalf("unresolved recovery erased the old session: %+v", during.Session)
	}
	if during.PendingCreate == nil || during.PendingCreate.ID != "old-create" {
		t.Fatalf("unresolved recovery erased the pending create: %+v", during.PendingCreate)
	}
	if during.Cleanup == nil || during.Cleanup.State != protocol.CleanupPending {
		t.Fatalf("unresolved recovery erased the old cleanup: %+v", during.Cleanup)
	}

	syncRecovery(t, e, readySync())
	waitRecoveryComplete(t, e)
	e.mu.Lock()
	session := e.session
	last := e.lastSession
	after := e.ledgerRecordLocked()
	e.mu.Unlock()
	if session != nil {
		t.Fatalf("resolved recovery kept a current session: %+v", session)
	}
	if last == nil || last.Origin != "Pold" {
		t.Fatalf("resolved recovery lost the retired session: %+v", last)
	}
	if after.Session != nil {
		t.Fatalf("resolved recovery still snapshots the old session: %+v", after.Session)
	}
	if after.PendingCreate != nil {
		t.Fatalf("resolved recovery still snapshots the pending create: %+v", after.PendingCreate)
	}
	if after.Cleanup == nil || after.Cleanup.State != protocol.CleanupComplete ||
		after.Cleanup.Execution != protocol.CleanupConfirmed {
		t.Fatalf("resolved cleanup record: %+v", after.Cleanup)
	}
	status, apiErr := e.Status("agent", "")
	if apiErr != nil || status.CanStart == nil || !*status.CanStart {
		t.Fatalf("start not admitted after recovery resolved: %+v %v", status, apiErr)
	}
}

func TestRecoveryRevalidatesAppAfterDeviceWait(t *testing.T) {
	base := newFakeDevice()
	base.displayExists = false
	entered, release := make(chan struct{}), make(chan struct{})
	// StartRecovery has no App, so it must not probe before the sync below.
	probeCalls := 0
	dev := &hookedDevice{Device: base, probe: func(ctx context.Context, id int) (device.DisplayState, error) {
		probeCalls++
		if probeCalls == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return device.DisplayState{}, ctx.Err()
			}
		}
		return base.ProbeDisplay(ctx, id)
	}}
	e := recoveryEngine(t, oldLedger(), dev)
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { syncRecovery(t, e, readySync()); close(done) }()
	awaitSignal(t, entered)
	e.mu.Lock()
	e.app.LastSync = time.Now().Add(-time.Minute)
	e.mu.Unlock()
	close(release)
	awaitSignal(t, done)
	waitRecoveryAttempt(t, e)
	assertCannotStart(t, e)
}
