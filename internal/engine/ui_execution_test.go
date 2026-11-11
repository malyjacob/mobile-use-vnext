package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// A successful remote launch call and its failed task attribution are separate
// facts. The original P remains uncertain, while its ended call cannot hold a
// later session's execution barrier closed.
func TestOpenAppAttributionFailureDoesNotRetainUIExecution(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		name := "missing_task"
		if multiple {
			name = "multiple_tasks"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarnessWithClock(t, false)
			first := h.startSession()
			h.device.mu.Lock()
			h.device.launchTask = &device.Task{TaskID: 77, Package: "com.example"}
			h.device.recentKnown = true
			h.device.recentIDs = map[int]bool{77: true}
			h.device.mu.Unlock()
			var hide atomic.Bool
			hide.Store(true)
			h.engine.dev = &hookedDevice{Device: h.device, tasks: func(ctx context.Context) ([]device.Task, error) {
				if !hide.Load() {
					return h.device.TaskList(ctx)
				}
				if multiple {
					return []device.Task{{TaskID: 77, DisplayID: 12, Package: "com.example"},
						{TaskID: 78, DisplayID: 12, Package: "com.example"}}, nil
				}
				return nil, nil
			}}
			opened, apiErr := h.engine.OpenApp(context.Background(), "agent", "Pattribution",
				openAppRequest(first.Observation, "com.example"))
			if apiErr != nil || opened.Outcome != OutcomeUncertain || opened.Error == nil || opened.Error.Code != protocol.CodeAppLaunchUnknown {
				t.Fatal("launch did not reach task attribution failure")
			}
			hide.Store(false)
			observed, apiErr := h.engine.Observe(context.Background(), "agent", "Pafterattribution",
				&protocol.ObserveRequest{Reference: opened.OperationID})
			if apiErr != nil || observed.Observation == nil || observed.Observation.Tree == nil {
				t.Fatal("completed launch call incorrectly blocked observation")
			}
			h.device.mu.Lock()
			h.device.displayExists = false
			h.device.mu.Unlock()
			if _, apiErr := h.engine.End("agent", "Pendedattribution", opened.OperationID); apiErr != nil {
				t.Fatal(apiErr)
			}
			h.engine.mu.Lock()
			done := h.engine.cleanup.done
			h.engine.mu.Unlock()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("physical cleanup did not finish")
			}
			h.sync(nil, nil)
			status, apiErr := h.engine.Status("agent", "")
			if apiErr != nil || status.Cleanup == nil || status.Cleanup.Execution != protocol.CleanupConfirmed || status.Cleanup.State != protocol.CleanupComplete || status.CanStart == nil || !*status.CanStart {
				t.Fatal("ended launch call retained execution barrier after confirmed physical cleanup")
			}
			original, apiErr := h.engine.Status("agent", opened.OperationID)
			if apiErr != nil || original.Outcome != OutcomeUncertain {
				t.Fatal("cleanup rewrote original outcome")
			}
		})
	}
}

// A closed input window is a recorded fact, never an admission barrier: the
// uncertain call must not forbid the observation that is the only way to
// narrow it.
func TestUnknownUIReceiptAllowsSubsequentOrdinaryWork(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	h.engine.dev = &hookedDevice{Device: h.device, tap: func(context.Context, int, int, int) error {
		return errors.New("synthetic transport failure")
	}}
	acted, apiErr := h.engine.Act(context.Background(), "agent", "Punknownui", tapRequest(first.Observation.ID))
	if apiErr != nil || acted.Outcome != OutcomeUncertain {
		t.Fatal("UI dispatch did not become uncertain")
	}
	h.engine.mu.Lock()
	token := h.engine.ui
	h.engine.mu.Unlock()
	if token == nil || token.record.State != uiStateUnknown {
		t.Fatalf("unknown receipt did not close the window: %+v", token)
	}
	before := h.device.captureCount()
	observed, apiErr := h.engine.Observe(context.Background(), "agent", "Pafterunknownobserve",
		&protocol.ObserveRequest{Reference: acted.OperationID})
	if apiErr != nil || observed.Error != nil || observed.Observation == nil || h.device.captureCount() == before {
		t.Fatalf("unknown receipt blocked the observation that narrows it: %+v %v", observed, apiErr)
	}
	if observed.Observation.ID == first.Observation.ID {
		t.Fatal("observation after the unknown receipt reused the consumed O")
	}
	h.device.setShellDone(exitedDone(0, 0, false))
	shelled, apiErr := h.engine.Shell(context.Background(), "agent", "Pafterunknownshell",
		&protocol.ShellRequest{Reference: acted.OperationID, Command: "true"})
	if apiErr != nil || shelled.Error != nil || shelled.Outcome != OutcomeExecuted {
		t.Fatalf("unknown receipt blocked a later shell: %+v %v", shelled, apiErr)
	}
	blockedStart, apiErr := h.engine.Start(context.Background(), "agent", "Pafterunknownstart")
	if apiErr != nil || blockedStart.Error == nil || blockedStart.Error.Code != protocol.CodeSessionExists {
		t.Error("an active session must still reject a second start as session_exists")
	}
	status, apiErr := h.engine.Status("agent", acted.OperationID)
	if apiErr != nil || status.Outcome != OutcomeUncertain {
		t.Fatal("read-only status unavailable or outcome rewritten")
	}
}

func TestLateUITerminationPreservesUnknownWithoutBudgetRefresh(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var taskReads atomic.Int32
	h.engine.dev = &hookedDevice{Device: h.device,
		tap: func(context.Context, int, int, int) error {
			close(entered)
			<-release
			return nil
		},
		tasks: func(ctx context.Context) ([]device.Task, error) {
			taskReads.Add(1)
			return h.device.TaskList(ctx)
		},
	}
	call := make(chan callResult, 1)
	go func() {
		r, err := h.engine.Act(context.Background(), "agent", "Plateui", tapRequest(first.Observation.ID))
		call <- callResult{r, err}
	}()
	awaitSignal(t, entered)
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.recentKnown = true
	h.device.mu.Unlock()
	if _, apiErr := h.engine.End("agent", "Pendlateui", first.OperationID); apiErr != nil {
		t.Fatal(apiErr)
	}
	h.engine.mu.Lock()
	done := h.engine.cleanup.done
	h.engine.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("physical cleanup did not settle independently")
	}
	h.engine.mu.Lock()
	var workUI *uiDispatchState
	if h.engine.work != nil {
		workUI = h.engine.work.UI
	}
	retiredOrigin := ""
	for _, retired := range h.engine.retired {
		if retired.cleanup == h.engine.cleanup || workUI != nil && retired.ui == workUI {
			retiredOrigin = retired.session.Origin
			break
		}
	}
	h.engine.mu.Unlock()
	if retiredOrigin == "" {
		t.Fatal("ended UI work was not present in retired ownership")
	}
	before := taskReads.Load()
	h.sync(nil, nil)
	once.Do(func() { close(release) })
	result := awaitCall(t, call)
	if result.Outcome != OutcomeUncertain || result.Observation != nil {
		t.Fatal("late UI termination rewrote result or published an observation")
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		h.engine.mu.Lock()
		var retiredCleanup *LedgerCleanup
		preserved := false
		for _, retired := range h.engine.retired {
			if retired.session != nil && retired.session.Origin == retiredOrigin &&
				retired.cleanup != nil &&
				retired.cleanup.Execution == protocol.CleanupConfirmed {
				retiredCleanup = ledgerCleanupOf(retired.cleanup)
				if retired.cleanup.needsExecutionRecheck {
					preserved = true
					break
				}
			}
		}
		if preserved {
			h.engine.mu.Unlock()
			break
		}
		currentCleanup := ledgerCleanupOf(h.engine.cleanup)
		currentUI := h.engine.ui
		ledger := h.engine.ledgerRecordLocked()
		change := h.engine.appNotify
		h.engine.mu.Unlock()
		select {
		case <-change:
		case <-time.After(20 * time.Millisecond):
		case <-deadline.C:
			t.Fatalf("late termination did not preserve its recheck obligation: retired=%+v ledger=%+v current=%+v ui=%+v", retiredCleanup, ledger.Retired, currentCleanup, currentUI)
		}
	}
	retired := retiredLedgerForOrigin(t, h.engine, retiredOrigin)
	if retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.State != protocol.CleanupUnconfirmed ||
		retired.Record.Cleanup.Execution != protocol.CleanupConfirmed ||
		!retired.Record.Cleanup.ExecutionRecheckRequired ||
		retired.Record.Cleanup.Tasks != protocol.CleanupUnconfirmed ||
		retired.Record.Cleanup.Recents != protocol.CleanupUnconfirmed ||
		retired.Record.Cleanup.Display != protocol.CleanupUnconfirmed {
		t.Fatalf("late termination fabricated a clean physical result: %+v", retired.Record.Cleanup)
	}
	if taskReads.Load() != before {
		t.Fatal("late termination refreshed the stopped cleanup budget with another physical read")
	}
	assertCanStart(t, h.engine)
	original, apiErr := h.engine.Status("agent", "Plateui")
	if apiErr != nil || original.Outcome != OutcomeUncertain {
		t.Fatal("late termination rewrote original uncertain outcome")
	}
}

type uiGatedStore struct {
	Store
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *uiGatedStore) Save(ctx context.Context, record *Ledger) (int64, error) {
	if record.UI != nil && record.UI.OperationID == "Pgatedui" && record.UI.State == "may_dispatch" {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return s.Store.Save(ctx, record)
}

func TestUIPreWriteDoesNotDelayStopOrDispatchAfterRevocation(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	gated := &uiGatedStore{Store: h.store, entered: make(chan struct{}), release: make(chan struct{})}
	h.engine.persistMu.Lock()
	h.engine.store = gated
	h.engine.persistMu.Unlock()
	call := make(chan callResult, 1)
	go func() {
		r, err := h.engine.Act(context.Background(), "agent", "Pgatedui", tapRequest(first.Observation.ID))
		call <- callResult{r, err}
	}()
	awaitSignal(t, gated.entered)
	started := time.Now()
	stopNow(t, h)
	if time.Since(started) > time.Second {
		t.Error("STOP waited for UI intent durability")
	}
	close(gated.release)
	result := awaitCall(t, call)
	if result.Outcome != OutcomeNotExecuted || result.Observation != nil {
		t.Fatal("revoked prewrite admitted a dispatched action")
	}
	h.device.mu.Lock()
	taps := len(h.device.taps)
	h.device.mu.Unlock()
	if taps != 0 {
		t.Fatal("device was touched after STOP revoked the prewrite")
	}
}

func TestTextFocusReadFailureDoesNotReopenEndedTap(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	h.engine.dev = &hookedDevice{Device: h.device, capture: func(ctx context.Context, display device.Display) (device.Capture, error) {
		h.device.mu.Lock()
		focused := h.device.inputFocused
		h.device.mu.Unlock()
		if focused {
			return device.Capture{}, errors.New("synthetic post-focus read failure")
		}
		return h.device.Capture(ctx, display)
	}}
	result, apiErr := h.engine.Act(context.Background(), "agent", "Pfocusread",
		&protocol.ActRequest{ObservationID: first.Observation.ID, Operation: protocol.Operation{
			Type: "text", Target: &protocol.Target{X: 500, Y: 350}, Text: "synthetic",
		}})
	if apiErr != nil || result.Outcome != OutcomeUncertain {
		t.Fatal("text target read failure did not preserve uncertain outcome")
	}
	h.engine.mu.Lock()
	pending := h.engine.uiPendingLocked()
	h.engine.mu.Unlock()
	h.device.mu.Lock()
	sets := len(h.device.texts)
	h.device.mu.Unlock()
	if pending || sets != 0 {
		t.Fatal("post-focus read failure reopened the terminated tap or wrote text")
	}
}

// Version 1 UI uncertainty is migrated into a v3 retired record. It remains
// unconfirmed without being copied into the current top-level ownership.
func TestLegacyUISyncRetiresMigrationNeed(t *testing.T) {
	record := oldLedger()
	record.Version = 1
	record.Session.State = SessionEnded
	record.Cleanup = &LedgerCleanup{State: protocol.CleanupUnconfirmed,
		Execution: protocol.CleanupUnconfirmed, ExecReason: "ui_input",
		Tasks: protocol.CleanupConfirmed, Recents: protocol.CleanupConfirmed, Display: protocol.CleanupConfirmed}
	e := recoveryEngine(t, record, newFakeDevice())
	if err := e.StartRecovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	syncRecovery(t, e, readySync())
	waitRecoveryAttempt(t, e)
	waitRetiredCleanupStopped(t, e, "Pold")
	assertCanStart(t, e)
	retired := retiredLedgerForOrigin(t, e, "Pold")
	if retired.Record.Version != ledgerVersion ||
		retired.Record.Cleanup == nil ||
		retired.Record.Cleanup.ExecReason != "ui_input" ||
		retired.Record.Cleanup.Execution != protocol.CleanupUnconfirmed ||
		retired.Record.Cleanup.State == protocol.CleanupComplete {
		t.Fatalf("legacy UI migration lost or confirmed its unknown fact: %+v", retired.Record)
	}
	e.mu.Lock()
	snapshot := e.ledgerRecordLocked()
	e.mu.Unlock()
	if snapshot.Session != nil || snapshot.UI != nil || snapshot.Cleanup != nil {
		t.Fatalf("legacy UI responsibility remained in current fields: %+v", snapshot)
	}
}

type uiFailTerminalStore struct{ Store }

func (s uiFailTerminalStore) Save(ctx context.Context, record *Ledger) (int64, error) {
	if record.UI != nil && record.UI.OperationID == "Pterminalwrite" && record.UI.State == "terminated" {
		return 0, errors.New("synthetic terminal evidence write failure")
	}
	return s.Store.Save(ctx, record)
}

func TestUITerminalWriteFailurePreservesExecutedOutcome(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	h.engine.persistMu.Lock()
	h.engine.store = uiFailTerminalStore{Store: h.store}
	h.engine.persistMu.Unlock()
	result, apiErr := h.engine.Act(context.Background(), "agent", "Pterminalwrite", tapRequest(first.Observation.ID))
	if apiErr != nil || result.Outcome != OutcomeExecuted {
		t.Error("a durability failure rewrote a proven completed input")
	}
	if result.Recovery == nil || result.Recovery.Entry != protocol.RecoveryEntryStatus || result.Recovery.Reference != protocol.RecoveryReferenceP {
		t.Error("terminal write failure omitted recovery from its executed result")
	}
	original, apiErr := h.engine.Status("agent", "Pterminalwrite")
	if apiErr != nil || original.Recovery == nil || original.Recovery.Entry != protocol.RecoveryEntryStatus || original.Recovery.Reference != protocol.RecoveryReferenceP {
		t.Error("status(P) omitted terminal durability recovery")
	}
	status, apiErr := h.engine.Status("agent", "")
	if apiErr != nil || status.CanStart == nil || *status.CanStart || status.BlockingCode != "ledger_unavailable" {
		t.Error("failed terminal durability did not keep ordinary admission closed")
	}
	if status.Recovery == nil || status.Recovery.Entry != protocol.RecoveryEntryStatus {
		t.Error("self status omitted ledger recovery guidance")
	}
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.mu.Unlock()
}

type uiNotSentDevice struct {
	Device
	focus bool
}

func (d uiNotSentDevice) FocusTask(ctx context.Context, taskID int) (device.DispatchReceipt, error) {
	if d.focus {
		return device.DispatchNotDispatched, errors.New("synthetic pre-dispatch rejection")
	}
	return d.Device.FocusTask(ctx, taskID)
}

func (d uiNotSentDevice) LaunchApp(context.Context, int, string) (device.LaunchResult, error) {
	return device.LaunchResult{State: device.LaunchNotStarted, Receipt: device.DispatchNotDispatched}, errors.New("synthetic pre-dispatch rejection")
}

func startUIEvidenceSession(t *testing.T, h *harness, primary bool) *protocol.Result {
	t.Helper()
	if !primary {
		return h.startSession()
	}
	h.engine.mu.Lock()
	h.engine.app.Mode = "primary"
	h.engine.app.Primary = &device.Display{ID: 0, Width: 1080, Height: 2400, Density: 420, Generation: h.modeGen}
	h.engine.mu.Unlock()
	first, apiErr := h.engine.Start(context.Background(), "agent", "Pprimaryevidence")
	if apiErr != nil || first.Outcome != OutcomeExecuted || first.Observation == nil {
		t.Fatal("primary test session did not start")
	}
	return first
}

func TestOpenAppNotDispatchedKeepsNotExecuted(t *testing.T) {
	for _, primary := range []bool{false, true} {
		for _, focus := range []bool{false, true} {
			t.Run(fmt.Sprintf("primary=%t/focus=%t", primary, focus), func(t *testing.T) {
				h := newHarnessWithClock(t, false)
				first := startUIEvidenceSession(t, h, primary)
				if focus {
					displayID := 12
					if primary {
						displayID = 0
					}
					h.device.tasks = []device.Task{{TaskID: 77, DisplayID: displayID, Package: "com.example"}}
				}
				h.engine.dev = uiNotSentDevice{Device: h.device, focus: focus}
				result, apiErr := h.engine.OpenApp(context.Background(), "agent", "Pnotdispatched", openAppRequest(first.Observation, "com.example"))
				if apiErr != nil || result.Outcome != OutcomeNotExecuted {
					t.Fatal("explicit non-dispatch was reported as a possibly executed action")
				}
				h.engine.mu.Lock()
				pending := h.engine.uiPendingLocked()
				h.engine.mu.Unlock()
				if pending {
					t.Fatal("non-dispatch retained execution responsibility")
				}
			})
		}
	}
}

func TestPrimaryFocusRequiresForegroundEvidence(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := startUIEvidenceSession(t, h, true)
	h.device.tasks = []device.Task{{TaskID: 77, DisplayID: 0, Package: "com.example"}}
	h.device.foreground = &device.Foreground{State: device.ForegroundUnknown, Reason: "synthetic"}
	result, apiErr := h.engine.OpenApp(context.Background(), "agent", "Pprimaryfocus", openAppRequest(first.Observation, "com.example"))
	if apiErr != nil || result.Outcome != OutcomeUncertain || result.Error == nil || result.Error.Code != protocol.CodeTaskFocus {
		t.Fatal("primary focus returned executed without foreground evidence")
	}
	h.engine.mu.Lock()
	pending := h.engine.uiPendingLocked()
	h.engine.mu.Unlock()
	if pending {
		t.Fatal("foreground read failure reopened a terminated focus call")
	}
}
