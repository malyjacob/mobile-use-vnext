package engine

import (
	"context"
	"errors"
	"strconv"
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
	// Swap the store under persistMu: persistAsync may read the interface from
	// another goroutine, and an unlocked interface write can be observed as a
	// torn value.
	h.engine.persistMu.Lock()
	h.engine.store = gate
	h.engine.persistMu.Unlock()
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

// Fixed service instance identities whose derived 4-digit O/P tags are known.
// They keep the restart-isolation assertions deterministic instead of relying
// on a random pair that could collide with probability 1/9000.
const (
	// shortTagInstanceA derives tag 6325.
	shortTagInstanceA = "11111111-1111-4111-8111-111111111111"
	// shortTagInstanceADuplicate derives the same tag 6325 as shortTagInstanceA
	// while being a different service identity.
	shortTagInstanceADuplicate = "c0000000-0000-4000-8000-000000009855"
	// shortTagInstanceB derives tag 1436 and shortTagInstanceBNext tag 1437.
	shortTagInstanceB     = "22222222-2222-4222-8222-222222222222"
	shortTagInstanceBNext = "d0000000-0000-4000-8000-000000000603"
)

func TestGeneratedReferencesDoNotRebindAfterRestart(t *testing.T) {
	if deriveShortInstanceTag(shortTagInstanceA) == deriveShortInstanceTag(shortTagInstanceB) {
		t.Fatal("test constants no longer derive different instance tags")
	}
	a := New(DefaultConfig(shortTagInstanceA, "agent"), nil, nil, nil, nil, nil)
	b := New(DefaultConfig(shortTagInstanceB, "agent"), nil, nil, nil, nil, nil)
	if a.nextObservationIDLocked() == b.nextObservationIDLocked() || a.nextOperationIDLocked() == b.nextOperationIDLocked() {
		t.Fatal("new service reused old O/P identity")
	}
}

func TestShortInstanceTagDerivation(t *testing.T) {
	stable := deriveShortInstanceTag(shortTagInstanceA)
	if stable != deriveShortInstanceTag(shortTagInstanceA) {
		t.Fatal("one instance identity derived two different tags")
	}
	if stable < shortInstanceTagMin || stable > shortInstanceTagMax {
		t.Fatalf("derived tag %d is outside [%d, %d]", stable, shortInstanceTagMin, shortInstanceTagMax)
	}
	text := strconv.Itoa(stable)
	if len(text) != 4 || text[0] == '0' {
		t.Fatalf("derived tag %q is not 4 digits without a leading zero", text)
	}
	// An absent identity still yields one in-range tag. The value is random, so
	// only the range and the 4-digit shape are asserted.
	anonymous := deriveShortInstanceTag("")
	if anonymous < shortInstanceTagMin || anonymous > shortInstanceTagMax {
		t.Fatalf("anonymous tag %d is outside [%d, %d]", anonymous, shortInstanceTagMin, shortInstanceTagMax)
	}
	// Guard the fixed constants the restart tests rely on: one deliberate
	// collision pair and one adjacent-tag pair.
	if deriveShortInstanceTag(shortTagInstanceA) != deriveShortInstanceTag(shortTagInstanceADuplicate) {
		t.Fatal("deliberate tag collision constants no longer collide")
	}
	if deriveShortInstanceTag(shortTagInstanceB)+1 != deriveShortInstanceTag(shortTagInstanceBNext) {
		t.Fatal("adjacent tag constants are no longer adjacent")
	}
}

func TestGeneratedReferencesUseShortInstanceTag(t *testing.T) {
	e := New(DefaultConfig(shortTagInstanceA, "agent"), nil, nil, nil, nil, nil)
	tag := strconv.Itoa(deriveShortInstanceTag(shortTagInstanceA))
	if got := e.instanceTagLocked(); got != tag {
		t.Fatalf("instance tag = %q, want %q", got, tag)
	}
	// The durable identity the control App validates stays a full UUID.
	if identity := e.InstanceID(); identity != shortTagInstanceA || len(identity) != 36 || !ValidateUUID(identity) {
		t.Fatalf("service instance identity changed: %q", identity)
	}
	if observation := e.nextObservationIDLocked(); observation != "O"+tag+"_1" {
		t.Fatalf("observation identity = %q, want O%s_1", observation, tag)
	}
	if operation := e.nextOperationIDLocked(); operation != "P"+tag+"_1" {
		t.Fatalf("operation identity = %q, want P%s_1", operation, tag)
	}
	// A second allocation advances only the sequence.
	if observation := e.nextObservationIDLocked(); observation != "O"+tag+"_2" {
		t.Fatalf("second observation identity = %q, want O%s_2", observation, tag)
	}
	if operation := e.nextOperationIDLocked(); !validOperationID(operation) || operation != "P"+tag+"_2" {
		t.Fatalf("second operation identity = %q, want a valid P%s_2", operation, tag)
	}
}

// instanceTagRecoveryEngine builds a service process for instanceID, loads
// record through the normal startup barrier and returns the engine.
func instanceTagRecoveryEngine(t *testing.T, instanceID string, record *Ledger) *Engine {
	t.Helper()
	cfg := DefaultConfig(instanceID, "agent")
	cfg.CleanupTimeout = 200 * time.Millisecond
	cfg.LedgerTimeout = 200 * time.Millisecond
	dev := newFakeDevice()
	dev.displayExists = false
	e := New(cfg, nil, dev, &fakeStore{record: record},
		&testProjector{builder: observation.NewBuilder()}, testDiffer{})
	e.SetDeviceReady(true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		e.Shutdown(ctx)
	})
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatalf("StartRecovery: %v", err)
	}
	return e
}

// TestRestartAvoidsCollidingInstanceTag proves the ledger collision rule: two
// service generations whose identities deliberately derive the same 4-digit tag
// must not reuse the older O/P prefixes after the ledger is loaded.
func TestRestartAvoidsCollidingInstanceTag(t *testing.T) {
	previousTag := deriveShortInstanceTag(shortTagInstanceA)
	if previousTag != deriveShortInstanceTag(shortTagInstanceADuplicate) {
		t.Fatalf("test constants no longer collide: %d vs %d",
			previousTag, deriveShortInstanceTag(shortTagInstanceADuplicate))
	}
	oldOrigin := "P" + strconv.Itoa(previousTag) + "_7"
	oldObservation := "O" + strconv.Itoa(previousTag) + "_9"
	record := oldLedger()
	record.ServiceInstanceID = shortTagInstanceA
	record.Session.Origin = oldOrigin

	e := instanceTagRecoveryEngine(t, shortTagInstanceADuplicate, record)

	wantTag := (previousTag-shortInstanceTagMin+1)%shortInstanceTagSpan + shortInstanceTagMin
	e.mu.Lock()
	tag, observations, operations := e.shortInstanceTag, e.nextObservation, e.nextOperation
	observation := e.nextObservationIDLocked()
	operation := e.nextOperationIDLocked()
	e.mu.Unlock()
	if observations != 0 || operations != 0 {
		t.Fatalf("recovery allocated identities before the tag was fixed: O=%d P=%d", observations, operations)
	}
	if tag != strconv.Itoa(wantTag) {
		t.Fatalf("restarted instance tag = %q, want the avoided successor %d", tag, wantTag)
	}
	if observation == oldObservation || operation == oldOrigin {
		t.Fatalf("restarted instance reused an older identity: %q %q", observation, operation)
	}
	if observation != "O"+strconv.Itoa(wantTag)+"_1" || operation != "P"+strconv.Itoa(wantTag)+"_1" {
		t.Fatalf("new identities = %q %q, want the avoided prefix", observation, operation)
	}
	// The durable identity is untouched: the control App still needs a UUID.
	if identity := e.InstanceID(); identity != shortTagInstanceADuplicate ||
		len(identity) != 36 || !ValidateUUID(identity) {
		t.Fatalf("restarted identity changed: %q", identity)
	}
}

// TestRecoveryAvoidsRetiredAndOriginTags proves each avoidance source on its
// own: the main record's session origin, a retired record's service instance
// identity and a retired record's session origin.
func TestRecoveryAvoidsRetiredAndOriginTags(t *testing.T) {
	currentTag := deriveShortInstanceTag(shortTagInstanceB)
	retiredTag := deriveShortInstanceTag(shortTagInstanceBNext)
	if currentTag+1 != retiredTag {
		t.Fatalf("test constants are no longer adjacent: %d vs %d", currentTag, retiredTag)
	}
	retiredOrigin := "P" + strconv.Itoa(currentTag+2) + "_3"
	cases := []struct {
		name            string
		retiredIdentity string
		retiredOrigins  []string
		wantTag         int
	}{
		// The main session origin blocks the current tag, the retired identity
		// blocks its successor and the retired origin blocks the one after that.
		{name: "identity and origin block in order", retiredIdentity: shortTagInstanceBNext,
			retiredOrigins: []string{retiredOrigin}, wantTag: currentTag + 3},
		{name: "retired identity alone is avoided", retiredIdentity: shortTagInstanceBNext,
			wantTag: currentTag + 2},
		{name: "retired origins alone are avoided",
			retiredOrigins: []string{
				"P" + strconv.Itoa(currentTag+1) + "_1", retiredOrigin,
			}, wantTag: currentTag + 3},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			record := oldLedger()
			// The loaded ledger belongs to this identity, so its own derived tag is
			// never the reason to avoid: only the retained session origin is.
			record.ServiceInstanceID = shortTagInstanceB
			record.Session.Origin = "P" + strconv.Itoa(currentTag) + "_1"
			if testCase.retiredIdentity != "" {
				record.Retired = append(record.Retired, LedgerRetired{Record: &Ledger{
					Format: ledgerFormat, Version: ledgerVersion,
					ServiceInstanceID: testCase.retiredIdentity,
					UpdatedAt:         time.Now().Add(-2 * time.Minute), AppInstanceID: testAppID,
					Session: &LedgerSession{Origin: "Poldretired", Agent: "agent", Mode: "virtual",
						State: SessionEnded, AppInstanceID: testAppID, StartedAt: time.Now().Add(-2 * time.Minute)},
				}})
			}
			for _, origin := range testCase.retiredOrigins {
				record.Retired = append(record.Retired, LedgerRetired{Record: &Ledger{
					Format: ledgerFormat, Version: ledgerVersion,
					UpdatedAt: time.Now().Add(-2 * time.Minute), AppInstanceID: testAppID,
					Session: &LedgerSession{Origin: origin, Agent: "agent", Mode: "primary",
						State: SessionEnded, AppInstanceID: testAppID, StartedAt: time.Now().Add(-2 * time.Minute)},
				}})
			}

			e := instanceTagRecoveryEngine(t, shortTagInstanceB, record)

			e.mu.Lock()
			tag := e.shortInstanceTag
			operation := e.nextOperationIDLocked()
			e.mu.Unlock()
			if tag != strconv.Itoa(testCase.wantTag) {
				t.Fatalf("chosen tag = %q, want %d", tag, testCase.wantTag)
			}
			if operation != "P"+strconv.Itoa(testCase.wantTag)+"_1" {
				t.Fatalf("new operation identity = %q", operation)
			}
			if operation == record.Session.Origin {
				t.Fatalf("restarted instance reused the retired session origin %q", operation)
			}
			for _, origin := range testCase.retiredOrigins {
				if operation == origin {
					t.Fatalf("restarted instance reused a retired session origin %q", operation)
				}
			}
			if identity := e.InstanceID(); len(identity) != 36 || !ValidateUUID(identity) {
				t.Fatalf("service instance identity is no longer a 36-character UUID: %q", identity)
			}
		})
	}
}
