package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type hookedProjector struct {
	Projector
	build func(*device.Capture) (*Projection, error)
	reset func()
}

func (p *hookedProjector) Build(c *device.Capture) (*Projection, error) {
	if p.build != nil {
		return p.build(c)
	}
	return p.Projector.Build(c)
}
func (p *hookedProjector) Reset() {
	if p.reset != nil {
		p.reset()
		return
	}
	p.Projector.Reset()
}

func TestCaptureCommitRejectsExpiryAndDisplayGenerationChange(t *testing.T) {
	for _, change := range []string{"lease", "hard_limit", "display_generation"} {
		t.Run(change, func(t *testing.T) {
			h := newHarness(t)
			start := h.startSession()
			base := h.engine.projector
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			h.engine.projector = &hookedProjector{Projector: base, build: func(c *device.Capture) (*Projection, error) {
				p, err := base.Build(c)
				close(entered)
				<-release
				return p, err
			}}
			done := make(chan callResult, 1)
			go func() {
				r, e := h.engine.Observe(context.Background(), "agent", "Pcommit", &protocol.ObserveRequest{Reference: start.Observation.ID})
				done <- callResult{r, e}
			}()
			awaitSignal(t, entered)
			h.engine.mu.Lock()
			switch change {
			case "lease":
				h.engine.session.ExpiresAt = time.Now().Add(-time.Second)
			case "hard_limit":
				h.engine.session.HardExpiresAt = time.Now().Add(-time.Second)
			case "display_generation":
				h.engine.session.Display.Generation = "changed"
			}
			h.engine.mu.Unlock()
			once.Do(func() { close(release) })
			r := awaitCall(t, done)
			if r.Observation != nil && r.Observation.ID != "" {
				t.Error("invalid candidate committed")
			}
			h.engine.mu.Lock()
			count := h.engine.nextObservation
			h.engine.mu.Unlock()
			if count != 1 {
				t.Errorf("late O allocated: %d", count)
			}
		})
	}
}

func TestProjectorResetDoesNotHoldStateLock(t *testing.T) {
	h := newHarness(t)
	base := h.engine.projector
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	h.engine.projector = &hookedProjector{Projector: base, reset: func() { close(entered); <-release; base.Reset() }}
	h.engine.mu.Lock()
	h.engine.app.Mode = "primary"
	h.engine.mu.Unlock()
	done := make(chan callResult, 1)
	go func() { r, e := h.engine.Start(context.Background(), "agent", "Preset"); done <- callResult{r, e} }()
	awaitSignal(t, entered)
	stopped := make(chan struct{})
	go func() { stopNow(t, h); close(stopped) }()
	awaitSignal(t, stopped)
	once.Do(func() { close(release) })
	awaitCall(t, done)
}

type blockingStore struct {
	Store
	entered, release chan struct{}
	once             sync.Once
}

func (s *blockingStore) Save(ctx context.Context, r *Ledger) (int64, error) {
	s.once.Do(func() {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	})
	return s.Store.Save(ctx, r)
}
func TestStopPassesLedgerWaitAndPreventsCreateDispatch(t *testing.T) {
	h := newHarness(t)
	// Drain the initial heartbeat save before installing the deterministic block.
	if err := h.engine.persistSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	blocked := &blockingStore{Store: h.store, entered: make(chan struct{}), release: make(chan struct{})}
	h.engine.store = blocked
	var once sync.Once
	defer once.Do(func() { close(blocked.release) })
	done := make(chan callResult, 1)
	go func() { r, e := h.engine.Start(context.Background(), "agent", "Pledger"); done <- callResult{r, e} }()
	awaitSignal(t, blocked.entered)
	stopped := make(chan struct{})
	go func() { stopNow(t, h); close(stopped) }()
	awaitSignal(t, stopped)
	once.Do(func() { close(blocked.release) })
	awaitCall(t, done)
	h.device.mu.Lock()
	calls := h.device.helperCalls
	h.device.mu.Unlock()
	if calls != 0 {
		t.Errorf("helper started after STOP crossed ledger wait: %d", calls)
	}
}

func TestObserveFullForcesFullAfterBaselineAcknowledgement(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	delta, err := h.engine.Observe(context.Background(), "agent", "Pdelta", &protocol.ObserveRequest{Reference: start.Observation.ID})
	if err != nil || delta.Observation == nil || delta.Observation.Tree == nil || delta.Observation.Tree.Delta == nil {
		t.Fatalf("baseline did not produce delta: %+v %v", delta, err)
	}
	full, err := h.engine.Observe(context.Background(), "agent", "Pfull", &protocol.ObserveRequest{Reference: delta.Observation.ID, Full: true})
	if err != nil || full.Observation == nil || full.Observation.Tree == nil || full.Observation.Tree.Full == nil {
		t.Fatalf("full=true ignored: %+v %v", full, err)
	}
}

// TestObserveFullFlagAndDeltaCostGate covers the full flag and the encoding
// cost gate together: after a confirmed baseline an unchanged tree yields a
// delta with full=false, full=true forces a full tree for that same baseline,
// and a delta that is not cheaper than 70% of the full encoding falls back to
// full even when full=false.
func TestObserveFullFlagAndDeltaCostGate(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	ack, err := h.engine.Observe(context.Background(), "agent", "Pdeltaack",
		&protocol.ObserveRequest{Reference: start.Observation.ID})
	if err != nil || ack.Observation == nil || ack.Observation.Tree == nil ||
		ack.Observation.Tree.Delta == nil {
		t.Fatalf("baseline acknowledgement did not return a cheap delta: %+v %v", ack, err)
	}
	unforced, err := h.engine.Observe(context.Background(), "agent", "Pdeltafree",
		&protocol.ObserveRequest{Reference: ack.Observation.ID, Full: false})
	if err != nil || unforced.Observation == nil || unforced.Observation.Tree == nil ||
		unforced.Observation.Tree.Delta == nil || unforced.Observation.Tree.Full != nil {
		t.Fatalf("full=false lost the available delta: %+v %v", unforced, err)
	}
	forced, err := h.engine.Observe(context.Background(), "agent", "Pfullforced",
		&protocol.ObserveRequest{Reference: unforced.Observation.ID, Full: true})
	if err != nil || forced.Observation == nil || forced.Observation.Tree == nil ||
		forced.Observation.Tree.Full == nil || forced.Observation.Tree.Delta != nil {
		t.Fatalf("full=true did not force a full tree: %+v %v", forced, err)
	}

	huge := strings.Repeat("x", 60*1024)
	base := h.device
	h.engine.dev = &hookedDevice{Device: base,
		capture: func(ctx context.Context, display device.Display) (device.Capture, error) {
			capture, captureErr := base.Capture(ctx, display)
			if captureErr != nil {
				return capture, captureErr
			}
			for i := range capture.Nodes {
				if capture.Nodes[i].ClassName == "android.widget.Button" {
					capture.Nodes[i].Text = huge
					capture.Nodes[i].TextSet = true
				}
			}
			return capture, nil
		}}
	cost, err := h.engine.Observe(context.Background(), "agent", "Pcostgate",
		&protocol.ObserveRequest{Reference: unforced.Observation.ID, Full: false})
	if err != nil || cost.Observation == nil || cost.Observation.Tree == nil ||
		cost.Observation.Tree.Full == nil || cost.Observation.Tree.Delta != nil {
		t.Fatalf("expensive delta was not replaced by full: %+v %v", cost, err)
	}
}

func TestExpiryStopAndEndHaveOneTerminalAndCleanup(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.engine.mu.Lock()
	s := h.engine.session
	s.ExpiresAt = time.Now().Add(-time.Second)
	h.engine.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); h.engine.housekeeping(context.Background()) }()
	go func() { defer wg.Done(); stopNow(t, h) }()
	go func() { defer wg.Done(); _, _ = h.engine.End("agent", "Pendrace", start.OperationID) }()
	wg.Wait()
	h.engine.mu.Lock()
	terminalAt, reason, cleanup := s.EndedAt, s.Reason, h.engine.cleanup
	h.engine.mu.Unlock()
	h.engine.housekeeping(context.Background())
	_, _ = h.engine.End("agent", "Pendrepeat", start.OperationID)
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if terminalAt.IsZero() || s.State != SessionEnded || !s.EndedAt.Equal(terminalAt) || s.Reason != reason {
		t.Error("terminal state was rewritten")
	}
	if cleanup == nil || h.engine.cleanup != cleanup {
		t.Error("terminal race started another cleanup")
	}
}

func TestGeneratedOperationIDSkipsCallerSuppliedID(t *testing.T) {
	h := newHarness(t)
	if _, _, err := h.engine.registerOperation("P1", "agent", "observe"); err != nil {
		t.Fatal(err)
	}
	op, dup, err := h.engine.registerOperation("", "agent", "act")
	if err != nil || dup != nil || op == nil || op.ID == "P1" {
		t.Fatalf("generated P collided: %+v %+v %v", op, dup, err)
	}
}
