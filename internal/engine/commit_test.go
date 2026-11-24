package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
	"github.com/malyjacob/mobile-use-vnext/internal/treetext"
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

// Candidate applies the same hook so a test that blocks inside projection also
// observes the ordinary acquisition path, which now builds a candidate.
func (p *hookedProjector) Candidate(c *device.Capture) (*Projection, func(), error) {
	if p.build != nil {
		projection, err := p.build(c)
		return projection, func() {}, err
	}
	return p.Projector.Candidate(c)
}
func (p *hookedProjector) Reset() {
	if p.reset != nil {
		p.reset()
		return
	}
	p.Projector.Reset()
}

func TestCaptureCommitRejectsExpiryAndDisplayGenerationChange(t *testing.T) {
	for _, change := range []string{"lease", "display_generation"} {
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
	// Swap the store under persistMu: persistAsync may read the interface from
	// another goroutine, and an unlocked interface write can be observed as a
	// torn value.
	h.engine.persistMu.Lock()
	h.engine.store = blocked
	h.engine.persistMu.Unlock()
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

func TestTreeTextPreviewDoesNotAllocateObservationID(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.engine.mu.Lock()
	before := h.engine.nextObservation
	preview := h.engine.previewObservationIDLocked()
	after := h.engine.nextObservation
	h.engine.mu.Unlock()
	if before != after {
		t.Fatalf("preview allocated an observation: %d -> %d", before, after)
	}

	h.device.scriptCaptures(replayCurrentCapture(t, h.engine))
	result, err := h.engine.Observe(context.Background(), "agent", "Ppreview",
		&protocol.ObserveRequest{Reference: start.Observation.ID, Full: true})
	if err != nil {
		t.Fatalf("observe after preview: %v", err)
	}
	if result.Observation == nil || result.Observation.ID != preview {
		t.Fatalf("published observation = %+v, want preview %q", result.Observation, preview)
	}
}

// TestObserveFullFlagAndDeltaCostGate covers the full flag and the encoding
// cost gate together: after a confirmed baseline an unchanged tree yields a
// delta with full=false, full=true forces a full tree for that same baseline,
// and a delta that is not cheaper than 80% of the full encoding falls back to
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

	// The expensive delta must be one the compact projection cannot shrink: an
	// input value keeps its full text (the actionable-label truncation only
	// applies to non-input nodes), so the delta stays larger than 80% of the full
	// block. A huge actionable button label would be truncated and would turn
	// this boundary check into a cheap delta.
	huge := strings.Repeat("x", 60*1024)
	base := h.device
	h.engine.dev = &hookedDevice{Device: base,
		capture: func(ctx context.Context, display device.Display) (device.Capture, error) {
			capture, captureErr := base.Capture(ctx, display)
			if captureErr != nil {
				return capture, captureErr
			}
			for i := range capture.Nodes {
				if capture.Nodes[i].ClassName == "android.widget.EditText" {
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

type fixedReadDiffer struct {
	delta *protocol.ReadDelta
}

func (d fixedReadDiffer) Diff(string, *protocol.ReadTree, *protocol.ReadTree) (*protocol.ReadDelta, error) {
	return d.delta, nil
}

func thresholdReadTree(changedBytes, stableBytes int) *protocol.ReadTree {
	changedLabel := []string{}
	if changedBytes > 0 {
		changedLabel = []string{strings.Repeat("x", changedBytes)}
	}
	stableLabel := []string{}
	if stableBytes > 0 {
		stableLabel = []string{strings.Repeat("s", stableBytes)}
	}
	changed := &protocol.ReadNode{
		Ref: "n2", Role: protocol.RoleButton, Bounds: protocol.HiddenBounds(),
		Label: changedLabel, Actions: []protocol.Action{protocol.ActionTap},
	}
	stable := &protocol.ReadNode{
		Ref: "n3", Role: protocol.RoleText, Bounds: protocol.HiddenBounds(),
		Label: stableLabel, Actions: []protocol.Action{},
	}
	return &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact,
		Root: &protocol.ReadNode{
			Ref: "n1", Role: protocol.RoleRoot, Bounds: protocol.HiddenBounds(),
			Label: []string{}, Actions: []protocol.Action{},
			Children: []*protocol.ReadNode{changed, stable},
		},
	}
}

// thresholdDeltaFixture uses two independent label lengths. The changed label
// contributes to both tree v5 blocks. The unchanged public label contributes
// only to the full block, which makes an exact 80% boundary constructible
// without changing topology.
func thresholdDeltaFixture(t *testing.T) (
	base, belowTree, equalTree *protocol.ReadTree,
	belowDelta, equalDelta *protocol.ReadDelta) {
	t.Helper()
	for stableBytes := 0; stableBytes <= 256; stableBytes++ {
		base = thresholdReadTree(1, stableBytes)
		probe := thresholdReadTree(2, stableBytes)
		probeDelta, err := observation.ReadDiff("Osynthetic", base, probe)
		if err != nil {
			t.Fatalf("probe threshold delta: %v", err)
		}
		probeFullText, ok := treetext.EncodeFull("Osynthetic", probe)
		if !ok {
			t.Fatal("encode threshold full as tree v5")
		}
		probeDeltaText, ok := treetext.EncodeDelta("Osynthetic", probeDelta)
		if !ok {
			t.Fatal("encode threshold delta as tree v5")
		}
		// Increasing the changed label by one byte raises 10*delta by ten and
		// 8*full by eight, closing this gap by two.
		gap := baselineDeltaNumerator*len(probeFullText) -
			baselineDeltaDenominator*len(probeDeltaText)
		if gap < 0 || gap%2 != 0 {
			continue
		}
		changedBytes := 2 + gap/2
		belowTree = thresholdReadTree(changedBytes-1, stableBytes)
		equalTree = thresholdReadTree(changedBytes, stableBytes)
		belowDelta, err = observation.ReadDiff("Osynthetic", base, belowTree)
		if err != nil {
			t.Fatalf("build below-boundary delta: %v", err)
		}
		equalDelta, err = observation.ReadDiff("Osynthetic", base, equalTree)
		if err != nil {
			t.Fatalf("build equal-boundary delta: %v", err)
		}
		belowFullText, ok := treetext.EncodeFull("Osynthetic", belowTree)
		if !ok {
			t.Fatal("encode below-boundary full as tree v5")
		}
		belowDeltaText, ok := treetext.EncodeDelta("Osynthetic", belowDelta)
		if !ok {
			t.Fatal("encode below-boundary delta as tree v5")
		}
		equalFullText, ok := treetext.EncodeFull("Osynthetic", equalTree)
		if !ok {
			t.Fatal("encode boundary full as tree v5")
		}
		equalDeltaText, ok := treetext.EncodeDelta("Osynthetic", equalDelta)
		if !ok {
			t.Fatal("encode boundary delta as tree v5")
		}
		if baselineDeltaDenominator*len(belowDeltaText) >=
			baselineDeltaNumerator*len(belowFullText) ||
			baselineDeltaDenominator*len(equalDeltaText) !=
				baselineDeltaNumerator*len(equalFullText) {
			continue
		}
		return base, belowTree, equalTree, belowDelta, equalDelta
	}
	t.Fatal("could not construct exact 80% delta/full boundary")
	return nil, nil, nil, nil, nil
}

func TestDeltaCostGateIsStrictlyBelowEightyPercent(t *testing.T) {
	base, belowTree, equalTree, belowDelta, equalDelta := thresholdDeltaFixture(t)
	for _, test := range []struct {
		name     string
		tree     *protocol.ReadTree
		delta    *protocol.ReadDelta
		wantFull bool
	}{
		{name: "below", tree: belowTree, delta: belowDelta},
		{name: "equal", tree: equalTree, delta: equalDelta, wantFull: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := &Engine{differ: fixedReadDiffer{delta: test.delta}}
			baseline := &baselineState{ObservationID: "Osynthetic", Tree: base}
			selected, deltaBytes, ok := engine.treeTextBaselineDelta(
				"Osynthetic", baseline, test.tree)
			if test.wantFull {
				if ok || selected != nil || deltaBytes != 0 {
					t.Fatalf("equal-cost delta was accepted: %+v bytes=%d", selected, deltaBytes)
				}
				return
			}
			if !ok || selected == nil {
				t.Fatal("strictly smaller delta was rejected")
			}
			encoded, encodedOK := treetext.EncodeDelta("Osynthetic", selected)
			if !encodedOK || len(encoded) != deltaBytes {
				t.Fatalf("measured delta bytes = %d, encoded = %d", deltaBytes, len(encoded))
			}
			applied, err := observation.ReadApply(base, selected)
			if err != nil {
				t.Fatalf("apply selected delta: %v", err)
			}
			if !observation.ReadEqual(applied, test.tree) {
				t.Fatal("selected delta did not reconstruct the current compact tree")
			}
		})
	}
}

func TestTreeTextDeltaMeasurementUsesUTF8BlockBytes(t *testing.T) {
	stable := strings.Repeat("界", 64)
	base := thresholdReadTree(1, 0)
	current := thresholdReadTree(2, 0)
	base.Root.Children[1].Label = []string{stable}
	current.Root.Children[1].Label = []string{stable}
	delta, err := observation.ReadDiff("Osynthetic", base, current)
	if err != nil {
		t.Fatalf("build UTF-8 delta: %v", err)
	}
	engine := &Engine{differ: fixedReadDiffer{delta: delta}}
	selected, deltaBytes, ok := engine.treeTextBaselineDelta("Osynthetic",
		&baselineState{ObservationID: "Osynthetic", Tree: base}, current)
	if !ok || selected == nil {
		t.Fatal("cheap UTF-8 candidate was rejected")
	}
	fullText, fullOK := treetext.EncodeFull("Osynthetic", current)
	deltaText, deltaOK := treetext.EncodeDelta("Osynthetic", selected)
	if !fullOK || !deltaOK {
		t.Fatal("encode UTF-8 tree-v5 blocks")
	}
	if !strings.HasSuffix(fullText, "end tree\n") || len(deltaText) != deltaBytes {
		t.Fatalf("tree-v5 block measurement lost its trailer: full=%q deltaBytes=%d",
			fullText[len(fullText)-min(len(fullText), 16):], deltaBytes)
	}
	if !strings.Contains(fullText, stable) {
		t.Fatal("full block did not retain the UTF-8 label bytes")
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
