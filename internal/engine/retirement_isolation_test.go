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

func TestRetiredCleanupFailureAllowsNewStartAndKeepsOriginalRoute(t *testing.T) {
	h := newHarnessWithClock(t, false)
	old := h.startSession()
	h.engine.mu.Lock()
	s := h.engine.session
	s.Tasks = []taskEntry{{TaskID: 41, DisplayID: 12, Proven: true}}
	h.engine.mu.Unlock()
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.recentKnown = true
	h.device.recentIDs = map[int]bool{41: true}
	h.device.removeErr = errors.New("synthetic removal failure")
	h.device.mu.Unlock()
	ended, err := h.engine.End("agent", "Pendold", old.OperationID)
	if err != nil || ended.Outcome != OutcomeExecuted {
		t.Fatal("end failed")
	}
	h.engine.mu.Lock()
	c := h.engine.cleanup
	h.engine.mu.Unlock()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not stop")
	}
	status, _ := h.engine.Status("agent", "")
	if !*status.CanStart || status.Cleanup.State != protocol.CleanupUnconfirmed || status.Cleanup.Recents != protocol.CleanupUnconfirmed {
		t.Fatalf("permanent gate: %+v", status)
	}
	h.device.mu.Lock()
	h.device.displayExists = true
	h.device.removeErr = nil
	h.device.mu.Unlock()
	current := h.startSession()
	original, err := h.engine.Status("agent", old.OperationID)
	if err != nil || original.Session.Origin != old.OperationID || original.Cleanup.Recents != protocol.CleanupUnconfirmed {
		t.Fatal("old P was rebound")
	}
	repeated, err := h.engine.End("agent", "Pendrepeat", old.OperationID)
	if err != nil || repeated.Session.Origin != old.OperationID {
		t.Fatal("historical end lost original owner")
	}
	// Even a successful late cleanup must not retire the new O or touch it.
	result := *c
	result.Execution, result.Tasks, result.Recents, result.Display = protocol.CleanupConfirmed, protocol.CleanupConfirmed, protocol.CleanupConfirmed, protocol.CleanupConfirmed
	h.engine.finishCleanup(targetOf(s), c, result)
	live, _ := h.engine.Status("agent", current.Observation.ID)
	if live.Session.State != SessionActive || live.InputState != protocol.RecoveryInputEffective {
		t.Fatal("old completion revoked new work")
	}
}

func TestRetiredHandoverBoundsWorkAndLateUIReceipt(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	op, _, _ := h.engine.registerOperation("Poldinput", "agent", "act")
	work, err := h.engine.acquireWork(op, WorkAct)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.engine.beginUIDispatch(work, "tap"); err != nil {
		t.Fatal(err)
	}
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.mu.Unlock()
	if _, err := h.engine.End("agent", "Pendinput", first.OperationID); err != nil {
		t.Fatal(err)
	}
	h.engine.mu.Lock()
	h.engine.work.RevokedAt = time.Now().Add(-retirementHandover)
	h.engine.session.EndedAt = time.Now().Add(-retirementHandover)
	h.engine.mu.Unlock()
	h.engine.housekeeping(context.Background())
	h.device.mu.Lock()
	h.device.displayExists = true
	h.device.mu.Unlock()
	next := h.startSession()
	h.engine.recordUIReceipt(work, device.DispatchTerminated)
	h.engine.releaseWork(work)
	status, _ := h.engine.Status("agent", next.Observation.ID)
	if status.Session.State != SessionActive || status.InputState != protocol.RecoveryInputEffective {
		t.Fatal("late old receipt changed current authorization")
	}
	old, _ := h.engine.Status("agent", op.ID)
	if old.Outcome != OutcomeUncertain {
		t.Fatal("late receipt changed frozen uncertain outcome")
	}
}

func TestRetiredCapacityNeverCreatesAdmissionGate(t *testing.T) {
	h := newHarnessWithClock(t, false)
	h.engine.mu.Lock()
	for n := 0; n < 12; n++ {
		s := &sessionState{Origin: NewUUID(), Agent: "agent", State: SessionEnded, Mode: "primary", Epoch: int64(n + 1), EndedAt: time.Now()}
		c := notApplicableCleanupState()
		c.State = protocol.CleanupUnconfirmed
		c.Execution = protocol.CleanupUnconfirmed
		h.engine.retireLocked(s, c)
	}
	count := len(h.engine.retired)
	h.engine.mu.Unlock()
	status, _ := h.engine.Status("agent", "")
	if count != 8 || !*status.CanStart {
		t.Fatalf("capacity=%d can_start=%v", count, *status.CanStart)
	}
}

func TestNewSessionDoesNotShareRetiredProjector(t *testing.T) {
	h := newHarnessWithClock(t, false)
	var projectors []*testProjector
	h.engine.projectorFactory = func() Projector {
		p := &testProjector{builder: observation.NewBuilder()}
		projectors = append(projectors, p)
		return p
	}
	first := h.startSession()
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.mu.Unlock()
	h.engine.End("agent", "Pendbuilder", first.OperationID)
	h.engine.mu.Lock()
	oldCleanup := h.engine.cleanup
	h.engine.mu.Unlock()
	select {
	case <-oldCleanup.done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not finish")
	}
	h.device.mu.Lock()
	h.device.displayExists = true
	h.device.mu.Unlock()
	next := h.startSession()
	if len(projectors) != 2 || projectors[0] == projectors[1] {
		t.Fatal("session projector shared")
	}
	// A late old projection/reset must not alter current conservative ref IDs.
	projectors[0].Reset()
	observed, err := h.engine.Observe(context.Background(), "agent", "Pnewbuilder", &protocol.ObserveRequest{Reference: next.Observation.ID})
	if err != nil || observed.Observation == nil || observed.Observation.Tree == nil || observed.Observation.Tree.Delta == nil || !observed.Observation.Tree.Delta.IsEmpty() {
		t.Fatal("old reset changed current ref continuity")
	}
}

func TestDiscardedCleanupCannotDispatchAfterLedgerWait(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	if err := h.engine.persistSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	gate := &blockingStore{Store: h.store, entered: make(chan struct{}), release: make(chan struct{})}
	h.engine.store = gate
	h.engine.mu.Lock()
	h.engine.session.Tasks = []taskEntry{{TaskID: 41, DisplayID: 12, Proven: true}}
	h.engine.mu.Unlock()
	h.device.mu.Lock()
	h.device.recentKnown = true
	h.device.recentIDs = map[int]bool{41: true}
	h.device.mu.Unlock()
	h.engine.End("agent", "Pdropcleanup", first.OperationID)
	awaitSignal(t, gate.entered)
	h.engine.mu.Lock()
	for _, r := range h.engine.retired {
		if r.cancel != nil {
			r.cancel()
		}
		stopCleanup(r.cleanup, "cleanup_capacity_exhausted")
	}
	h.engine.retired = nil
	h.engine.cleanup = nil
	h.engine.session = nil
	h.engine.mu.Unlock()
	close(gate.release)
	h.engine.wg.Wait()
	if err := h.engine.persistSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.engine.mu.Lock()
	commands := len(h.engine.commands)
	h.engine.mu.Unlock()
	h.device.mu.Lock()
	removals := len(h.device.removeCalls)
	h.device.mu.Unlock()
	if commands != 0 || removals != 0 {
		t.Fatalf("discarded cleanup dispatched commands=%d removals=%d", commands, removals)
	}
}

func TestRetiredDisplayReusePreventsTaskDeletion(t *testing.T) {
	h := newHarnessWithClock(t, false)
	h.engine.mu.Lock()
	h.engine.retired = append(h.engine.retired, &retiredState{session: &sessionState{Origin: "Pnewowner", Epoch: 2, Display: device.Display{ID: 12}}, cleanup: &cleanupState{State: protocol.CleanupUnconfirmed, Display: protocol.CleanupUnconfirmed}})
	h.engine.mu.Unlock()
	h.device.mu.Lock()
	h.device.recentKnown = true
	h.device.recentIDs = map[int]bool{41: true}
	h.device.mu.Unlock()
	tasks, recents, _ := h.engine.removeOwnedTasksIdentity(context.Background(), 12, []taskEntry{{TaskID: 41, DisplayID: 12, Proven: true}}, device.BootIdentity{}, 1)
	h.device.mu.Lock()
	calls := len(h.device.removeCalls)
	h.device.mu.Unlock()
	if tasks || recents || calls != 0 {
		t.Fatal("old cleanup touched a reused retired display")
	}
}

func TestGeneratedReferencesDoNotRebindAfterRestart(t *testing.T) {
	a := New(DefaultConfig(NewUUID(), "agent"), nil, nil, nil, nil, nil)
	b := New(DefaultConfig(NewUUID(), "agent"), nil, nil, nil, nil, nil)
	if a.nextObservationIDLocked() == b.nextObservationIDLocked() || a.nextOperationIDLocked() == b.nextOperationIDLocked() {
		t.Fatal("new service reused old O/P identity")
	}
}
