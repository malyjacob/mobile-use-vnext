package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Hooks wrap the existing fake without changing its interface. Channels hold
// the actual adapter boundary, rather than relying on scheduling delays.
type hookedDevice struct {
	Device
	tap     func(context.Context, int, int, int) error
	capture func(context.Context, device.Display) (device.Capture, error)
	probe   func(context.Context, int) (device.DisplayState, error)
	tasks   func(context.Context) ([]device.Task, error)
	helper  func(context.Context, string, string) error
}

func (d *hookedDevice) StartDisplayHelper(ctx context.Context, commandID, appInstanceID string) error {
	if d.helper != nil {
		return d.helper(ctx, commandID, appInstanceID)
	}
	return d.Device.StartDisplayHelper(ctx, commandID, appInstanceID)
}

func (d *hookedDevice) Tap(ctx context.Context, id, x, y int) (device.DispatchReceipt, error) {
	if d.tap != nil {
		err := d.tap(ctx, id, x, y)
		if err != nil {
			return device.DispatchUnknown, err
		}
		return device.DispatchTerminated, nil
	}
	return d.Device.Tap(ctx, id, x, y)
}
func (d *hookedDevice) Capture(ctx context.Context, display device.Display) (device.Capture, error) {
	if d.capture != nil {
		return d.capture(ctx, display)
	}
	return d.Device.Capture(ctx, display)
}
func (d *hookedDevice) ProbeDisplay(ctx context.Context, id int) (device.DisplayState, error) {
	if d.probe != nil {
		return d.probe(ctx, id)
	}
	return d.Device.ProbeDisplay(ctx, id)
}
func (d *hookedDevice) TaskList(ctx context.Context) ([]device.Task, error) {
	if d.tasks != nil {
		return d.tasks(ctx)
	}
	return d.Device.TaskList(ctx)
}

type callResult struct {
	result *protocol.Result
	err    *protocol.APIError
}

func awaitCall(t *testing.T, ch <-chan callResult) *protocol.Result {
	t.Helper()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("API error: %v", got.err)
		}
		return got.result
	case <-time.After(3 * time.Second):
		t.Fatal("engine call blocked")
		return nil
	}
}
func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("adapter boundary not reached")
	}
}
func tapRequest(id string) *protocol.ActRequest {
	return &protocol.ActRequest{ObservationID: id, Operation: protocol.Operation{Type: "tap", Target: &protocol.Target{X: 20, Y: 20}}}
}
func stopNow(t *testing.T, h *harness) {
	t.Helper()
	_, err := h.engine.Stop(context.Background(), &protocol.ControlStopRequest{AppInstanceID: h.appInstance, StopSeq: 1, Reason: "user_stop"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStopDuringDispatchedActKeepsUncertain(t *testing.T) {
	for _, end := range []bool{false, true} {
		name := "stop"
		if end {
			name = "end"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			start := h.startSession()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			h.engine.dev = &hookedDevice{Device: h.device, tap: func(context.Context, int, int, int) error {
				close(entered)
				<-release
				return nil // late success after cancellation is not timely evidence
			}}
			done := make(chan callResult, 1)
			go func() {
				r, e := h.engine.Act(context.Background(), "agent", "Pflight", tapRequest(start.Observation.ID))
				done <- callResult{r, e}
			}()
			awaitSignal(t, entered)
			if end {
				if _, err := h.engine.End("agent", "Pend", start.OperationID); err != nil {
					t.Fatal(err)
				}
			} else {
				stopNow(t, h)
			}
			h.engine.mu.Lock()
			unknown := h.engine.work != nil && h.engine.work.DispatchUnknown
			cleanExec := ""
			if h.engine.cleanup != nil {
				cleanExec = h.engine.cleanup.Execution
			}
			h.engine.mu.Unlock()
			once.Do(func() { close(release) })
			r := awaitCall(t, done)
			if !unknown || cleanExec != protocol.CleanupUnconfirmed {
				t.Errorf("in-flight input incorrectly confirmed: unknown=%v cleanup.execution=%s", unknown, cleanExec)
			}
			if r.Outcome != OutcomeUncertain {
				t.Errorf("late input completion became %q", r.Outcome)
			}
			if r.Observation != nil && r.Observation.ID != "" {
				t.Errorf("late observation published: %+v", r.Observation)
			}
			h.engine.mu.Lock()
			defer h.engine.mu.Unlock()
			if h.engine.work != nil {
				t.Error("ordinary work leaked")
			}
			if h.engine.observations[start.Observation.ID].Status != ObservationRetired {
				t.Error("claimed observation not retired")
			}
			for _, c := range h.engine.commands {
				if c.Kind == "create_display" {
					t.Error("create command survived revocation")
				}
			}
		})
	}
}

func TestStopDuringPostCaptureDoesNotPublishOrRewriteExecuted(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	calls := 0
	h.engine.dev = &hookedDevice{Device: h.device, capture: func(ctx context.Context, d device.Display) (device.Capture, error) {
		calls++
		if calls == 2 {
			close(entered)
			<-release
		}
		return h.device.Capture(ctx, d)
	}}
	done := make(chan callResult, 1)
	go func() {
		r, e := h.engine.Act(context.Background(), "agent", "Ppost", tapRequest(start.Observation.ID))
		done <- callResult{r, e}
	}()
	awaitSignal(t, entered)
	stopNow(t, h)
	once.Do(func() { close(release) })
	r := awaitCall(t, done)
	if r.Outcome != OutcomeExecuted {
		t.Errorf("confirmed input lost its outcome: %q", r.Outcome)
	}
	if r.Observation != nil && r.Observation.ID != "" {
		t.Error("post-STOP candidate published")
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.nextObservation != 1 {
		t.Errorf("allocated late O: %d", h.engine.nextObservation)
	}
}

func TestStartProbeCannotReviveStoppedSession(t *testing.T) {
	h := newHarness(t)
	h.engine.mu.Lock()
	h.engine.app.Mode = "primary"
	h.engine.mu.Unlock()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var probeOnce sync.Once
	h.engine.dev = &hookedDevice{Device: h.device, probe: func(context.Context, int) (device.DisplayState, error) {
		probeOnce.Do(func() { close(entered) })
		<-release
		return device.DisplayState{Exists: true, SFID: 1}, nil
	}}
	done := make(chan callResult, 1)
	go func() { r, e := h.engine.Start(context.Background(), "agent", "Pstartstop"); done <- callResult{r, e} }()
	awaitSignal(t, entered)
	stopNow(t, h)
	once.Do(func() { close(release) })
	r := awaitCall(t, done)
	if r.Outcome != OutcomeNotExecuted {
		t.Errorf("start revoked during the display probe reported %q", r.Outcome)
	}
	if r.Observation != nil && r.Observation.ID != "" {
		t.Error("revoked start published O")
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.nextObservation != 0 {
		t.Errorf("revoked start allocated O: %d", h.engine.nextObservation)
	}
	if h.engine.session != nil && h.engine.session.State != SessionEnded {
		t.Errorf("stopped session revived: %s", h.engine.session.State)
	}
}

func TestConcurrentClaimHasSingleWinner(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	h.engine.dev = &hookedDevice{Device: h.device, tap: func(ctx context.Context, id, x, y int) error {
		close(entered)
		<-release
		_, err := h.device.Tap(ctx, id, x, y)
		return err
	}}
	done := make(chan callResult, 1)
	go func() {
		r, e := h.engine.Act(context.Background(), "agent", "Pwinner", tapRequest(start.Observation.ID))
		done <- callResult{r, e}
	}()
	awaitSignal(t, entered)
	loser, err := h.engine.Act(context.Background(), "agent", "Ploser", tapRequest(start.Observation.ID))
	if err != nil || loser.Outcome != OutcomeNotExecuted {
		t.Fatalf("second claim accepted: %+v %v", loser, err)
	}
	h.engine.mu.Lock()
	claimedBy := h.engine.observations[start.Observation.ID].ClaimedBy
	h.engine.mu.Unlock()
	if claimedBy != "Pwinner" {
		t.Errorf("loser changed claim owner: %s", claimedBy)
	}
	once.Do(func() { close(release) })
	winner := awaitCall(t, done)
	if winner.Outcome != OutcomeExecuted {
		t.Fatalf("winner failed: %+v", winner)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	ready := 0
	for _, o := range h.engine.observations {
		if o.Status == ObservationReady {
			ready++
		}
	}
	if ready != 1 || h.engine.nextObservation != 2 {
		t.Errorf("ready=%d observations=%d", ready, h.engine.nextObservation)
	}
}

func TestConcurrentSamePAcrossObserveAndActDoesNotDispatchTwice(t *testing.T) {
	for _, winner := range []string{"observe", "act"} {
		t.Run(winner, func(t *testing.T) {
			h := newHarness(t)
			start := h.startSession()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			h.engine.dev = &hookedDevice{Device: h.device, capture: func(ctx context.Context, d device.Display) (device.Capture, error) {
				close(entered)
				<-release
				return h.device.Capture(ctx, d)
			}}
			done := make(chan callResult, 1)
			go func() {
				var r *protocol.Result
				var e *protocol.APIError
				if winner == "observe" {
					r, e = h.engine.Observe(context.Background(), "agent", "Pshared", &protocol.ObserveRequest{Reference: start.OperationID})
				} else {
					r, e = h.engine.Act(context.Background(), "agent", "Pshared", tapRequest(start.Observation.ID))
				}
				done <- callResult{r, e}
			}()
			awaitSignal(t, entered)
			var dup *protocol.Result
			var err *protocol.APIError
			if winner == "observe" {
				dup, err = h.engine.Act(context.Background(), "agent", "Pshared", tapRequest(start.Observation.ID))
			} else {
				dup, err = h.engine.Observe(context.Background(), "agent", "Pshared", &protocol.ObserveRequest{Reference: start.OperationID})
			}
			if err != nil || dup.OperationState != OperationRunning {
				t.Fatalf("duplicate did not return original P: %+v %v", dup, err)
			}
			// Stop avoids a second capture by the winning act, and proves duplicates
			// neither consume another O nor re-dispatch after termination.
			stopNow(t, h)
			once.Do(func() { close(release) })
			awaitCall(t, done)
			h.engine.mu.Lock()
			defer h.engine.mu.Unlock()
			if h.engine.nextObservation != 1 {
				t.Error("duplicate published an O")
			}
			if winner == "observe" && h.engine.observations[start.Observation.ID].ClaimedBy != "" {
				t.Error("losing act consumed O")
			}
		})
	}
}

func TestExpiredLeaseCannotBeRenewedOrPublished(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.engine.mu.Lock()
	expired := time.Now().Add(-time.Second)
	h.engine.session.ExpiresAt = expired
	h.engine.touchSessionLocked(time.Now())
	got := h.engine.session.ExpiresAt
	h.engine.mu.Unlock()
	if !got.Equal(expired) {
		t.Error("expired lease was revived by activity")
	}
	r, err := h.engine.Act(context.Background(), "agent", "Pexpired", tapRequest(start.Observation.ID))
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != OutcomeNotExecuted {
		t.Errorf("expired lease dispatched input: %+v", r)
	}
}

func TestSessionLeaseSlidesOnlyUntilHardLimit(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	now := time.Now()
	h.engine.mu.Lock()
	s := h.engine.session
	s.ExpiresAt = now.Add(time.Second)
	s.HardExpiresAt = now.Add(30 * time.Second)
	h.engine.touchSessionLocked(now)
	first := s.ExpiresAt
	h.engine.revokeSessionLocked("user_stop", now)
	h.engine.touchSessionLocked(now.Add(time.Second))
	afterStop := s.ExpiresAt
	h.engine.mu.Unlock()
	if !first.Equal(now.Add(30*time.Second)) || !afterStop.Equal(first) {
		t.Errorf("lease first=%v afterStop=%v", first, afterStop)
	}
}

func TestCleanupDoesNotRestartAfterCompletion(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	h.engine.mu.Lock()
	s := h.engine.revokeSessionLocked("client_end", time.Now())
	first := h.engine.beginCleanupLocked(s, "client_end")
	first.State = protocol.CleanupComplete
	again := h.engine.beginCleanupLocked(s, "user_stop")
	h.engine.mu.Unlock()
	if again != nil {
		t.Fatal("cleanup was started twice for one session")
	}
}

// TestSessionLeaseStopsRenewalAtHardLimit isolates the hard-limit clamp: a
// fresh lease may slide up to HardExpiresAt, but once that instant is reached
// touchSessionLocked must never push it further, and an already expired lease
// must not be revived.
func TestSessionLeaseStopsRenewalAtHardLimit(t *testing.T) {
	h := newHarnessWithClock(t, false)
	h.startSession()
	now := time.Now()
	h.engine.mu.Lock()
	s := h.engine.session
	s.ExpiresAt = now.Add(time.Second)
	s.HardExpiresAt = now.Add(5 * time.Second)
	hard := s.HardExpiresAt
	h.engine.touchSessionLocked(now) // clamped to the hard limit
	clamped := s.ExpiresAt
	h.engine.touchSessionLocked(now.Add(4 * time.Second)) // still before the hard limit
	beforeHard := s.ExpiresAt
	h.engine.touchSessionLocked(now.Add(6 * time.Second)) // past the hard limit
	afterHard := s.ExpiresAt
	h.engine.mu.Unlock()
	if !clamped.Equal(hard) {
		t.Errorf("first renewal clamped to %v, want hard limit %v", clamped, hard)
	}
	if !beforeHard.Equal(hard) {
		t.Errorf("renewal near the hard limit moved expiry to %v, want %v", beforeHard, hard)
	}
	if !afterHard.Equal(hard) {
		t.Errorf("renewal after the hard limit revived expiry to %v, want %v", afterHard, hard)
	}
}

// TestExpiredSessionRejectsActAndObserveWithoutObservation keeps the lease
// expired without running housekeeping, so the request-time lazy check (not a
// background revocation) must reject both ordinary actions and allocate no O.
func TestExpiredSessionRejectsActAndObserveWithoutObservation(t *testing.T) {
	h := newHarnessWithClock(t, false)
	start := h.startSession()
	h.engine.mu.Lock()
	h.engine.session.ExpiresAt = time.Now().Add(-time.Second)
	h.engine.mu.Unlock()

	act, apiErr := h.engine.Act(context.Background(), "agent", "Pexpiredact",
		tapRequest(start.Observation.ID))
	if apiErr != nil {
		t.Fatalf("expired act transport error: %v", apiErr)
	}
	if act.Outcome != OutcomeNotExecuted || act.Error == nil ||
		act.Error.Code != protocol.CodeSessionExpired {
		t.Fatalf("expired act was not rejected: %+v", act)
	}

	observe, apiErr := h.engine.Observe(context.Background(), "agent", "Pexpiredobserve",
		&protocol.ObserveRequest{Reference: start.Observation.ID})
	if apiErr != nil {
		t.Fatalf("expired observe transport error: %v", apiErr)
	}
	if observe.Error == nil {
		t.Fatalf("expired observe returned no rejection: %+v", observe)
	}
	if observe.Observation != nil && observe.Observation.ID != "" {
		t.Fatalf("expired observe published an observation: %+v", observe.Observation)
	}

	h.engine.mu.Lock()
	next := h.engine.nextObservation
	status := h.engine.observations[start.Observation.ID].Status
	h.engine.mu.Unlock()
	if next != 1 {
		t.Errorf("rejected requests allocated O: next=%d", next)
	}
	if status != ObservationReady {
		t.Errorf("rejected act consumed the observation: %s", status)
	}
}

// TestStopAfterUnknownReceiptKeepsOutcomeAndClosesWindow covers the window
// after an acted input returned without an end receipt. The window is closed by
// the call itself: the P keeps its uncertain outcome, no in-flight execution
// remains to guard the closure, and new work is admitted after finite handover.
func TestStopAfterUnknownReceiptKeepsOutcomeAndClosesWindow(t *testing.T) {
	for _, terminal := range []string{"stop", "end"} {
		t.Run(terminal, func(t *testing.T) {
			h := newHarness(t)
			start := h.startSession()
			h.engine.dev = &hookedDevice{Device: h.device,
				tap: func(context.Context, int, int, int) error {
					return errors.New("adapter timeout after dispatch")
				}}
			result, apiErr := h.engine.Act(context.Background(), "agent", "Punknown",
				tapRequest(start.Observation.ID))
			if apiErr != nil {
				t.Fatalf("uncertain act transport error: %v", apiErr)
			}
			if result.Outcome != OutcomeUncertain {
				t.Fatalf("dispatched input with lost result became %q", result.Outcome)
			}
			h.engine.mu.Lock()
			released := h.engine.work == nil
			next := h.engine.nextObservation
			h.engine.mu.Unlock()
			if !released {
				t.Fatal("uncertain act did not release the ordinary work slot")
			}
			if next != 1 {
				t.Fatalf("uncertain act allocated O: %d", next)
			}

			if terminal == "stop" {
				stopNow(t, h)
			} else if _, err := h.engine.End("agent", "Pendafterunknown",
				start.OperationID); err != nil {
				t.Fatalf("end transport error: %v", err)
			}

			h.engine.mu.Lock()
			cleanup := h.engine.cleanup
			window := ""
			for _, retired := range h.engine.retired {
				if retired.session.Origin == start.OperationID && retired.ui != nil {
					window = retired.ui.record.State
				}
			}
			h.engine.mu.Unlock()
			if cleanup == nil {
				t.Fatal("no cleanup record after terminal request")
			}
			if cleanup.Execution != protocol.CleanupConfirmed {
				t.Fatalf("cleanup.execution=%q, want confirmed once the input call returned",
					cleanup.Execution)
			}
			if window != uiStateUnknown {
				t.Fatalf("closed input window state=%q, want unknown", window)
			}
			status, apiErr := h.engine.Status("agent", "Punknown")
			if apiErr != nil || status.Outcome != OutcomeUncertain {
				t.Fatalf("unknown act was rewritten: %+v %v", status, apiErr)
			}
			// The uncertain outcome is a retired fact; it does not block the
			// next session after the current STOP boundary synchronizes.
			awaitShellCondition(t, time.Second, "current STOP synchronization did not settle", func() bool {
				h.sync(nil, nil)
				status, _ := h.engine.Status("agent", "")
				return status.CanStart != nil && *status.CanStart
			})
			h.device.mu.Lock()
			h.device.displayExists = true
			h.device.mu.Unlock()
			second := h.startSession()
			if second.Outcome != OutcomeExecuted {
				t.Fatal("retired input blocked new start")
			}
			old, _ := h.engine.Status("agent", "Punknown")
			if old.Outcome != OutcomeUncertain || old.Cleanup == nil ||
				old.Cleanup.Execution != protocol.CleanupConfirmed {
				t.Fatal("new start erased or rewrote the original uncertainty")
			}

		})
	}
}

// TestStopAfterExecutedActKeepsExecutionConfirmed guards the opposite side of
// the execution flag: a timely confirmed input must not turn cleanup
// unconfirmed merely because STOP follows it.
func TestStopAfterExecutedActKeepsExecutionConfirmed(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	result, apiErr := h.engine.Act(context.Background(), "agent", "Pconfirmed", &protocol.ActRequest{
		ObservationID: start.Observation.ID,
		Operation:     protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref}},
	})
	if apiErr != nil || result.Outcome != OutcomeExecuted {
		t.Fatalf("confirmed act failed: %+v %v", result, apiErr)
	}
	stopNow(t, h)
	h.engine.mu.Lock()
	cleanup := h.engine.cleanup
	h.engine.mu.Unlock()
	if cleanup == nil || cleanup.Execution != protocol.CleanupConfirmed {
		t.Fatalf("confirmed input was reported as unconfirmed: %+v", cleanup)
	}
}

// TestStopBeforeDisplayProbeNeverAdmitsStart blocks the display helper, stops
// the starting session, then releases the helper: the start must abort without
// probing the display or publishing an O.
func TestStopBeforeDisplayProbeNeverAdmitsStart(t *testing.T) {
	h := newHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	base := h.device
	var probeMu sync.Mutex
	probes := 0
	h.engine.dev = &hookedDevice{Device: base,
		probe: func(ctx context.Context, id int) (device.DisplayState, error) {
			probeMu.Lock()
			if id != 0 {
				probes++
			}
			probeMu.Unlock()
			return base.ProbeDisplay(ctx, id)
		},
		helper: func(ctx context.Context, commandID, appInstanceID string) error {
			enterOnce.Do(func() { close(entered) })
			<-release
			return base.StartDisplayHelper(ctx, commandID, appInstanceID)
		}}
	done := make(chan callResult, 1)
	go func() {
		r, e := h.engine.Start(context.Background(), "agent", "Phelperstop")
		done <- callResult{r, e}
	}()
	awaitSignal(t, entered)
	stopNow(t, h)
	releaseOnce.Do(func() { close(release) })
	r := awaitCall(t, done)
	if r.Outcome != OutcomeNotExecuted {
		t.Errorf("start revoked during the helper reported %q", r.Outcome)
	}
	if r.Observation != nil && r.Observation.ID != "" {
		t.Errorf("revoked start published an observation: %+v", r.Observation)
	}
	h.engine.mu.Lock()
	next := h.engine.nextObservation
	session := h.engine.session
	h.engine.mu.Unlock()
	if next != 0 {
		t.Errorf("revoked start allocated O: %d", next)
	}
	if session != nil && session.State != SessionEnded {
		t.Errorf("revoked start left session %s", session.State)
	}
	probeMu.Lock()
	probed := probes
	probeMu.Unlock()
	if probed != 0 {
		t.Errorf("display was probed after STOP revoked the start: %d probes", probed)
	}
}

// TestStopAfterDisplayProbeDoesNotPublishStartObservation stops the first
// observation capture that runs after a successful display probe. The start may
// report the already-created session as executed, but it must publish no O and
// leave no active session behind.
func TestStopAfterDisplayProbeDoesNotPublishStartObservation(t *testing.T) {
	h := newHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	base := h.device
	h.engine.dev = &hookedDevice{Device: base,
		capture: func(ctx context.Context, display device.Display) (device.Capture, error) {
			enterOnce.Do(func() { close(entered) })
			<-release
			return base.Capture(ctx, display)
		}}
	done := make(chan callResult, 1)
	h.reportDisplayAfter(30 * time.Millisecond)
	go func() {
		r, e := h.engine.Start(context.Background(), "agent", "Pafterprobe")
		done <- callResult{r, e}
	}()
	awaitSignal(t, entered)
	stopNow(t, h)
	releaseOnce.Do(func() { close(release) })
	r := awaitCall(t, done)
	if r.Observation != nil && r.Observation.ID != "" {
		t.Errorf("start published an O after STOP: %+v", r.Observation)
	}
	h.engine.mu.Lock()
	next := h.engine.nextObservation
	session := h.engine.session
	h.engine.mu.Unlock()
	if next != 0 {
		t.Errorf("revoked start allocated O: %d", next)
	}
	if session == nil || session.State != SessionEnded {
		t.Errorf("start left a usable session after STOP: %+v", session)
	}
	second, apiErr := h.engine.Start(context.Background(), "agent", "Pafterprobe2")
	if apiErr != nil {
		t.Fatalf("second start transport error: %v", apiErr)
	}
	if second.Outcome == OutcomeExecuted {
		t.Errorf("start admitted before the App re-synced after STOP: %+v", second)
	}
}

var _ = errors.New
