package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// syncBuffer lets a test read the event sink without racing its writer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitForEvent polls the sink because the logger writes on its own goroutine.
func waitForEvent(t *testing.T, sink *syncBuffer, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if text := sink.String(); strings.Contains(text, want) {
			return text
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event %q was not recorded: %s", want, sink.String())
	return ""
}

// A work deadline freezes its own operation and releases the work position. It
// is not a session fact.
func TestWorkDeadlineDetachesOnlyTheWorkPosition(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	h.engine.mu.Lock()
	op := &operationState{ID: "Pstuckhandler", Agent: "agent", Kind: "observe",
		State: OperationRunning, Phase: "capture", SessionOrigin: first.OperationID,
		CreatedAt: time.Now()}
	h.engine.operations[op.ID] = op
	ctx, cancel := context.WithCancel(context.Background())
	h.engine.work = &workState{OperationID: op.ID, Agent: "agent", Kind: WorkObserve,
		Phase: "capture", SessionEpoch: h.engine.session.Epoch, Start: time.Now().Add(-time.Minute),
		Deadline: time.Now().Add(-time.Second), Ctx: ctx, Cancel: cancel,
		Projector: h.engine.projector}
	h.engine.mu.Unlock()

	h.engine.housekeeping(context.Background())

	h.engine.mu.Lock()
	workReleased := h.engine.work == nil
	state, reason := h.engine.session.State, h.engine.session.Reason
	opState, opPhase, opOutcome := op.State, op.Phase, op.Outcome
	obsStatus := h.engine.current.Status
	h.engine.mu.Unlock()
	if !workReleased {
		t.Fatal("work deadline did not release the ordinary work position")
	}
	if state != SessionActive || reason != "" {
		t.Fatalf("work deadline changed the session: state=%s reason=%s", state, reason)
	}
	if opState != OperationFinished || opPhase != "work_deadline" || opOutcome != OutcomeNotExecuted {
		t.Fatalf("abandoned operation not frozen: %s/%s/%s", opState, opPhase, opOutcome)
	}
	if obsStatus != ObservationReady {
		t.Fatalf("work deadline changed an observation: %s", obsStatus)
	}
	if ctx.Err() == nil {
		t.Fatal("abandoned handler was not cancelled")
	}
	observed, apiErr := h.engine.Observe(context.Background(), "agent", "Pafterdetach",
		&protocol.ObserveRequest{Reference: first.Observation.ID})
	if apiErr != nil || observed.Error != nil || observed.Observation == nil {
		t.Fatalf("new observation blocked after the work deadline: %+v %v", observed, apiErr)
	}
}

// The unknown end receipt is a durable closed-window fact and a single log
// event. It never becomes a second deadline or an admission gate.
func TestUnknownUIReceiptPersistsClosedWindow(t *testing.T) {
	sink := &syncBuffer{}
	log := eventlog.New(sink, "instance-test")
	h := newHarnessWithLogger(t, false, log)
	h.engine.log = log
	first := h.startSession()
	h.engine.dev = &hookedDevice{Device: h.device, tap: func(context.Context, int, int, int) error {
		return errors.New("synthetic transport failure")
	}}
	acted, apiErr := h.engine.Act(context.Background(), "agent", "Punknownreceipt",
		tapRequest(first.Observation.ID))
	if apiErr != nil || acted.Outcome != OutcomeUncertain {
		t.Fatalf("act did not reach the unknown receipt: %+v %v", acted, apiErr)
	}
	record, err := h.store.Load()
	if err != nil || record == nil || record.UI == nil {
		t.Fatalf("unknown receipt was not persisted: %+v %v", record, err)
	}
	if record.UI.State != uiStateUnknown {
		t.Fatalf("persisted window state=%q, want %q", record.UI.State, uiStateUnknown)
	}
	text := waitForEvent(t, sink, "ui.window_closed")
	if !strings.Contains(text, `state="unknown"`) {
		t.Fatalf("window event does not carry its closed state: %s", text)
	}
	// No second deadline source: the token has no time bound at all.
	h.engine.mu.Lock()
	pending := h.engine.uiPendingLocked()
	h.engine.mu.Unlock()
	if pending {
		t.Fatal("a returned call is still reported as pending input")
	}
}

// An unconfirmed start keeps the exact-chain barrier, but only until the
// confirmation bound; after it the chain is closed out and admission reopens.
func TestShellStartAmbiguityReleasesAdmissionAtTheBound(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	h.engine.cfg.ShellTimeout = 20 * time.Millisecond
	h.engine.cfg.ShellConfirmTimeout = 20 * time.Millisecond
	h.device.shell.mu.Lock()
	h.device.shell.startErr = errors.New("synthetic ambiguous start")
	h.device.shell.mu.Unlock()

	shelled, apiErr := h.engine.Shell(context.Background(), "agent", "Pshellbound",
		shellRequestFor(first.Observation, "synthetic"))
	if apiErr != nil || shelled.Outcome != OutcomeUncertain {
		t.Fatalf("unresolved shell result = %+v, api error = %v", shelled, apiErr)
	}
	h.engine.mu.Lock()
	run := h.engine.shell
	h.engine.mu.Unlock()
	if run == nil || run.settled() {
		t.Fatalf("unconfirmed start must keep its bounded barrier: %+v", run)
	}
	inside, apiErr := h.engine.Observe(context.Background(), "agent", "Pinsidebudget",
		&protocol.ObserveRequest{Reference: first.Observation.ID})
	if apiErr != nil || inside.Error == nil || inside.Error.Code != protocol.CodeDeviceBusy {
		t.Fatalf("barrier did not hold inside its budget: %+v %v", inside, apiErr)
	}

	h.engine.mu.Lock()
	run.DeadlineAt = time.Now().Add(-time.Minute)
	h.engine.mu.Unlock()
	h.engine.housekeeping(context.Background())

	h.engine.mu.Lock()
	live := h.engine.shell
	orphan := h.engine.shellOrphan
	state := h.engine.session.State
	h.engine.mu.Unlock()
	if live != nil {
		t.Fatal("expired bound did not release the work position")
	}
	if orphan != run || orphan.State != ShellUnknown || orphan.Termination != terminationUnknown {
		t.Fatalf("closed-out chain missing its orphan identity: %+v", orphan)
	}
	if state != SessionActive {
		t.Fatalf("closed-out chain changed the session: %s", state)
	}
	observed, apiErr := h.engine.Observe(context.Background(), "agent", "Pafterbound",
		&protocol.ObserveRequest{Reference: first.Observation.ID})
	if apiErr != nil || observed.Error != nil || observed.Observation == nil {
		t.Fatalf("admission stayed closed after the bound: %+v %v", observed, apiErr)
	}
}

// A timed-out chain whose termination cannot be confirmed is closed out at
// once. The session stays active, the work position is released and the
// closure must never report a confirmed execution while the orphan may still
// run.
func TestShellTimeoutCloseOutKeepsExecutionUnconfirmed(t *testing.T) {
	h := newHarnessWithClock(t, false)
	first := h.startSession()
	h.engine.cfg.ShellTimeout = 20 * time.Millisecond
	h.engine.cfg.ShellConfirmTimeout = 20 * time.Millisecond

	shelled, apiErr := h.engine.Shell(context.Background(), "agent", "Pshelltimeout",
		shellRequestFor(first.Observation, "sleep 70"))
	if apiErr != nil || shelled.Outcome != OutcomeUncertain || shelled.Error == nil ||
		shelled.Error.Code != protocol.CodeShellUnknown {
		t.Fatalf("timed-out shell result = %+v, api error = %v", shelled, apiErr)
	}
	h.engine.mu.Lock()
	live := h.engine.shell
	orphan := h.engine.shellOrphan
	workReleased := h.engine.work == nil
	state := h.engine.session.State
	h.engine.mu.Unlock()
	if live != nil || orphan == nil || orphan.State != ShellUnknown {
		t.Fatalf("timed-out chain was not closed out: live=%+v orphan=%+v", live, orphan)
	}
	if !workReleased {
		t.Fatal("timed-out chain kept the ordinary work position")
	}
	if state != SessionActive {
		t.Fatalf("timed-out chain changed the session: %s", state)
	}
	_, _, _, _, terminatesBefore, _ := h.device.shellCalls()

	if _, apiErr := h.engine.End("agent", "Pendorunconfirmed", first.OperationID); apiErr != nil {
		t.Fatalf("end rejected: %v", apiErr)
	}
	h.engine.mu.Lock()
	done := h.engine.cleanup.done
	h.engine.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("physical cleanup did not settle")
	}
	h.engine.mu.Lock()
	cleanup := h.engine.cleanup
	h.engine.mu.Unlock()
	if cleanup == nil {
		t.Fatal("no cleanup record")
	}
	if cleanup.Execution == protocol.CleanupConfirmed {
		t.Fatalf("closed-out chain fabricated a confirmed execution: %+v", cleanup)
	}
	_, _, _, _, terminatesAfter, _ := h.device.shellCalls()
	if terminatesAfter <= terminatesBefore {
		t.Fatal("the closure chain did not get its one more termination attempt")
	}
	if terminatesAfter != terminatesBefore+1 {
		t.Fatalf("orphan termination attempts=%d, want exactly one more", terminatesAfter-terminatesBefore)
	}
	original, apiErr := h.engine.Status("agent", "Pshelltimeout")
	if apiErr != nil || original.Outcome != OutcomeUncertain {
		t.Fatal("close-out rewrote the original uncertain outcome")
	}
}
