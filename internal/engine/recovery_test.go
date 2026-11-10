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

func assertCanStart(t *testing.T, e *Engine) {
	t.Helper()
	r, err := e.Status("agent", "")
	if err != nil || r.CanStart == nil || !*r.CanStart {
		t.Fatalf("recovery retained a permanent ordinary gate: %+v %v", r, err)
	}
}

func retiredLedgerForOrigin(t *testing.T, e *Engine, origin string) LedgerRetired {
	t.Helper()
	e.mu.Lock()
	record := e.ledgerRecordLocked()
	e.mu.Unlock()
	for _, retired := range record.Retired {
		if retired.Record != nil && retired.Record.Session != nil &&
			retired.Record.Session.Origin == origin {
			return retired
		}
	}
	t.Fatalf("retired record for %s was not preserved: %+v", origin, record.Retired)
	return LedgerRetired{}
}

func waitRetiredCleanupStopped(t *testing.T, e *Engine, origin string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		e.mu.Lock()
		stopped := false
		for _, retired := range e.retired {
			if retired.session != nil && retired.session.Origin == origin &&
				retired.cleanup != nil && retired.cleanup.State != protocol.CleanupPending {
				stopped = true
				break
			}
		}
		change := e.appNotify
		e.mu.Unlock()
		if stopped {
			return
		}
		select {
		case <-change:
		case <-deadline.C:
			t.Fatal("retired cleanup did not stop")
		}
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

func TestRecoveryRetiresUnresolvedTasksWithoutPermanentGate(t *testing.T) {
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
			waitRetiredCleanupStopped(t, e, "Pold")
			assertCanStart(t, e)
			retired := retiredLedgerForOrigin(t, e, "Pold")
			if retired.Record.Session == nil || retired.Record.Session.DisplayID != 12 {
				t.Fatalf("retired record lost the original session: %+v", retired.Record)
			}
			if retired.Record.Cleanup == nil ||
				retired.Record.Cleanup.Tasks != protocol.CleanupUnconfirmed ||
				retired.Record.Cleanup.Recents != protocol.CleanupUnconfirmed {
				t.Fatalf("unread task facts were not preserved as unknown: %+v", retired.Record.Cleanup)
			}
			base.mu.Lock()
			removals := len(base.removeCalls)
			base.mu.Unlock()
			if removals != 0 {
				t.Fatalf("recovery deleted a task without same-boot ownership evidence: %v", removals)
			}
			e.mu.Lock()
			recovering := e.recovery != nil
			e.mu.Unlock()
			if recovering {
				t.Error("lost unresolved task ownership")
			}
		})
	}
}

func TestRecoveryRetiresUnconfirmedFactsWithoutPermanentGate(t *testing.T) {
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
			waitRetiredCleanupStopped(t, e, "Pold")
			assertCanStart(t, e)
			retired := retiredLedgerForOrigin(t, e, "Pold")
			if retired.Record.Cleanup == nil {
				t.Fatal("retired cleanup fact was lost")
			}
			if field == "execution" &&
				retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed {
				t.Fatalf("stale execution was promoted without new evidence: %+v", retired.Record.Cleanup)
			}
			if field == "recents" &&
				retired.Record.Cleanup.Recents != protocol.CleanupUnconfirmed {
				t.Fatalf("stale recents were promoted without new evidence: %+v", retired.Record.Cleanup)
			}
			if retired.Record.Cleanup.State == protocol.CleanupComplete {
				t.Fatalf("unknown cleanup was reported complete: %+v", retired.Record.Cleanup)
			}
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
	req.Display = &protocol.AppDisplay{DisplayID: 12, Width: 1080, Height: 2400, Density: 420, Generation: "old-create"}
	syncRecovery(t, e, req)
	waitRecoveryAttempt(t, e)
	assertCanStart(t, e)
	var r *protocol.AppSyncResponse
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		r = syncRecovery(t, e, req)
		if len(r.Commands) > 0 {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-deadline.C:
			t.Fatalf("precise recovery close was not delivered: %+v", r.Commands)
		}
	}
	if len(r.Commands) != 1 || r.Commands[0].Kind != "close_display" || r.Commands[0].TargetAppInstanceID != testAppID || r.Commands[0].TargetDisplayID == nil || *r.Commands[0].TargetDisplayID != 12 || r.Commands[0].TargetGeneration != "old-create" {
		t.Fatalf("precise recovery close was not delivered: %+v", r.Commands)
	}
	if r.Control.Availability.State != AvailabilityReady {
		t.Fatalf("retired cleanup kept current availability recovering: %+v", r.Control)
	}
	retired := retiredLedgerForOrigin(t, e, "Pold")
	if retired.Record.PendingCreate == nil || retired.Record.PendingCreate.ID != "old-create" {
		t.Fatalf("retired record lost the precise create identity: %+v", retired.Record.PendingCreate)
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

// TestRecoveryMovesOldOwnershipIntoRetired inspects the pre-sync migration
// snapshot and the v3 retired record. Ended ownership must never remain in the
// top-level current fields, while the original session, create and cleanup
// evidence remain available for bounded compensation.
func TestRecoveryMovesOldOwnershipIntoRetired(t *testing.T) {
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
	waitRetiredCleanupStopped(t, e, "Pold")
	e.mu.Lock()
	after := e.ledgerRecordLocked()
	e.mu.Unlock()
	if after.Session != nil {
		t.Fatalf("v3 current fields still snapshot the ended session: %+v", after.Session)
	}
	if after.PendingCreate != nil {
		t.Fatalf("v3 current fields still snapshot the retired create: %+v", after.PendingCreate)
	}
	if after.Cleanup != nil {
		t.Fatalf("v3 current fields still snapshot retired cleanup: %+v", after.Cleanup)
	}
	if len(after.Retired) != 1 {
		t.Fatalf("retired records=%+v, want exactly the migrated ownership", after.Retired)
	}
	retired := after.Retired[0]
	if retired.Record == nil || retired.Record.Session == nil ||
		retired.Record.Session.Origin != "Pold" ||
		retired.Record.Session.DisplayID != 12 {
		t.Fatalf("retired record lost the original session: %+v", retired.Record)
	}
	if retired.Record.PendingCreate == nil || retired.Record.PendingCreate.ID != "old-create" {
		t.Fatalf("retired record lost the pending create: %+v", retired.Record.PendingCreate)
	}
	if retired.Record.Cleanup == nil {
		t.Fatal("retired record lost cleanup evidence")
	}
	if !retired.BudgetIssued {
		t.Fatal("migrated recovery record did not record its consumed cleanup budget")
	}
	assertCanStart(t, e)
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
	status, apiErr := e.Status("agent", "")
	if apiErr != nil || status.CanStart == nil || *status.CanStart ||
		status.BlockingCode != protocol.CodeControlUnavailable {
		t.Fatalf("stale App was replaced by an old-resource gate: %+v %v", status, apiErr)
	}
	syncRecovery(t, e, readySync())
	assertCanStart(t, e)
}
