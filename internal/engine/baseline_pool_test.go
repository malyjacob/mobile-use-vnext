package engine

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ---------------------------------------------------------------- page builder

// baselineRows builds a full-screen frame with count uniquely labeled rows. Rows
// carry only text, so the continuity pipeline matches them by content.
func baselineRows(prefix string, count int) []rawNodeSpec {
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
	}
	for index := 0; index < count; index++ {
		top := 100 + index*120
		specs = append(specs, rawNodeSpec{
			Parent: 0,
			Bounds: device.Rect{Left: 40, Top: top, Right: 1040, Bottom: top + 100},
			Class:  "android.widget.TextView",
			Text:   fmt.Sprintf("%s-%02d", prefix, index),
		})
	}
	return specs
}

// baselineBannerRows is baselineRows plus one temporary banner the reverse
// control removes and re-adds.
func baselineBannerRows() []rawNodeSpec {
	specs := baselineRows("row", 12)
	return append(specs, rawNodeSpec{
		Parent: 0,
		Bounds: device.Rect{Left: 40, Top: 1600, Right: 1040, Bottom: 1700},
		Class:  "android.widget.TextView",
		Text:   "temporary-banner",
	})
}

// baselinePageStep returns a capture step publishing exactly the given page.
func baselinePageStep(specs []rawNodeSpec) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return buildCapture(display, specs), nil
	}
}

// baselineObserve performs one admitted observe and returns the published
// observation.
func baselineObserve(t *testing.T, h *harness, p, reference string) *protocol.Observation {
	t.Helper()
	result, apiErr := h.engine.Observe(context.Background(), "agent", p,
		&protocol.ObserveRequest{Reference: reference})
	if apiErr != nil {
		t.Fatalf("observe %s rejected: %v", p, apiErr)
	}
	if result.Observation == nil || result.Observation.Error != nil {
		t.Fatalf("observe %s published no observation: %+v", p, result.Observation)
	}
	return result.Observation
}

func requireFullTree(t *testing.T, obs *protocol.Observation, revision int) {
	t.Helper()
	if obs == nil || obs.Revision != revision || obs.Tree == nil || obs.Tree.Full == nil {
		t.Fatalf("observation = %+v, want a full tree at revision %d", obs, revision)
	}
}

func requireBaselineDelta(t *testing.T, obs *protocol.Observation,
	revision int, baseID string) *protocol.ReadDelta {
	t.Helper()
	if obs == nil || obs.Revision != revision || obs.Tree == nil || obs.Tree.Delta == nil {
		t.Fatalf("observation = %+v, want a delta at revision %d", obs, revision)
	}
	if obs.Tree.Delta.Base != baseID {
		t.Fatalf("delta base = %q, want %q", obs.Tree.Delta.Base, baseID)
	}
	return obs.Tree.Delta
}

// readNodeByLabel returns the first node carrying the exact label.
func readNodeByLabel(root *protocol.ReadNode, label string) *protocol.ReadNode {
	if root == nil {
		return nil
	}
	for _, value := range root.Label {
		if value == label {
			return root
		}
	}
	for _, child := range root.Children {
		if found := readNodeByLabel(child, label); found != nil {
			return found
		}
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// findAddedLabel returns the ref of one added node carrying the exact label.
func findAddedLabel(nodes []*protocol.ReadNode, label string) string {
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, value := range node.Label {
			if value == label {
				return node.Ref
			}
		}
	}
	return ""
}

// TestBaselinePoolPageReturnWindow covers the bounded confirmed-full pool: a
// page return inside the four-step window is an empty delta against the page's
// own baseline, the same page keeps that base, and the fifth step slides the
// anchor out of the window and falls back to a full tree.
func TestBaselinePoolPageReturnWindow(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.MaxObservations = 3
	pageA := baselineRows("row", 12)
	pageB := baselineRows("beta", 4)
	pageAChanged := append(append([]rawNodeSpec(nil), pageA...), rawNodeSpec{
		Parent: 0,
		Bounds: device.Rect{Left: 40, Top: 1700, Right: 1040, Bottom: 1800},
		Class:  "android.widget.TextView",
		Text:   "row-extra",
	})

	h.device.scriptCaptures(baselinePageStep(pageA))
	start := h.startSession()
	o1 := start.Observation
	requireFullTree(t, o1, 1)

	h.device.scriptCaptures(baselinePageStep(pageB))
	o2 := baselineObserve(t, h, "PbaselinePageB", o1.ID)
	requireFullTree(t, o2, 2)
	// The confirmed page A baseline must stay readable while it is pooled.
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{Reference: o1.ID}); apiErr != nil {
		t.Fatalf("inspect pooled baseline: %v", apiErr)
	}

	h.device.scriptCaptures(baselinePageStep(pageA))
	o3 := baselineObserve(t, h, "PbaselinePageReturn", o2.ID)
	back := requireBaselineDelta(t, o3, 3, o1.ID)
	if !back.IsEmpty() {
		t.Fatalf("page return produced changes: %+v", back)
	}

	// Revision 4 stays on the page A baseline, and the unprotected intermediate
	// observation is evicted while the pooled baselines remain protected.
	h.device.scriptCaptures(baselinePageStep(pageAChanged))
	o4 := baselineObserve(t, h, "PbaselineSamePage", o3.ID)
	requireBaselineDelta(t, o4, 4, o1.ID)
	h.engine.mu.Lock()
	_, o3Retained := h.engine.observations[o3.ID]
	h.engine.mu.Unlock()
	if o3Retained {
		t.Fatalf("unprotected observation %s survived retention", o3.ID)
	}
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{Reference: o1.ID}); apiErr != nil {
		t.Fatalf("inspect protected baseline after eviction pressure: %v", apiErr)
	}

	h.device.scriptCaptures(baselinePageStep(pageAChanged))
	o5 := baselineObserve(t, h, "PbaselineSpanFour", o4.ID)
	requireBaselineDelta(t, o5, 5, o1.ID)

	// Revision 6 is outside the four-step window: page A can no longer anchor a
	// delta and the attempt publishes a full tree.
	h.device.scriptCaptures(baselinePageStep(pageA))
	o6 := baselineObserve(t, h, "PbaselineSpanFive", o5.ID)
	requireFullTree(t, o6, 6)

	// The discarded probes never shifted the ref high-water mark: after the new
	// full is confirmed, the same page is again an empty delta.
	h.device.scriptCaptures(baselinePageStep(pageA))
	o7 := baselineObserve(t, h, "PbaselineAfterFull", o6.ID)
	after := requireBaselineDelta(t, o7, 7, o6.ID)
	if !after.IsEmpty() {
		t.Fatalf("post-full continuity was polluted: %+v", after)
	}
}

// TestBaselinePoolPrimaryPathDoesNotResurrectBanner is the reverse control: a
// primary delta below 70% never enters the historical probe, so a temporary
// banner that disappears and reappears gets a fresh ref instead of its old one.
func TestBaselinePoolPrimaryPathDoesNotResurrectBanner(t *testing.T) {
	h := newHarness(t)
	withBanner := baselineBannerRows()
	withoutBanner := baselineRows("row", 12)

	h.device.scriptCaptures(baselinePageStep(withBanner))
	start := h.startSession()
	o1 := start.Observation
	requireFullTree(t, o1, 1)
	banner := readNodeByLabel(o1.Tree.Full.Root, "temporary-banner")
	if banner == nil {
		t.Fatal("fixture has no banner node")
	}

	h.device.scriptCaptures(baselinePageStep(withoutBanner))
	o2 := baselineObserve(t, h, "PbannerGone", o1.ID)
	gone := requireBaselineDelta(t, o2, 2, o1.ID)
	if gone.IsEmpty() {
		t.Fatal("banner removal produced an empty delta")
	}

	h.device.scriptCaptures(baselinePageStep(withBanner))
	o3 := baselineObserve(t, h, "PbannerBack", o2.ID)
	back := requireBaselineDelta(t, o3, 3, o1.ID)
	if back.IsEmpty() {
		t.Fatal("banner reappearance matched the primary baseline")
	}
	if !containsString(back.Remove, banner.Ref) {
		t.Fatalf("old banner ref %q was not removed: %+v", banner.Ref, back)
	}
	fresh := findAddedLabel(back.Add, "temporary-banner")
	if fresh == "" {
		t.Fatalf("reappeared banner was not added: %+v", back)
	}
	if fresh == banner.Ref {
		t.Fatalf("banner ref %q was resurrected instead of allocated fresh", banner.Ref)
	}
}

// TestBaselinePoolDisplayGenerationFallsBackToFull proves a display generation
// change invalidates the old ref continuity: the old baselines are never used
// for a delta, are pruned for the new display identity, and the observation
// stays readable.
func TestBaselinePoolDisplayGenerationFallsBackToFull(t *testing.T) {
	h := newHarness(t)
	pageA := baselineRows("row", 6)
	h.device.scriptCaptures(baselinePageStep(pageA))
	start := h.startSession()
	o1 := start.Observation

	h.device.scriptCaptures(baselinePageStep(pageA))
	o2 := baselineObserve(t, h, "PgenerationBaseline", o1.ID)
	requireBaselineDelta(t, o2, 2, o1.ID)

	h.engine.mu.Lock()
	h.engine.session.Display.Generation = "44444444-4444-4444-8444-444444444444"
	h.engine.mu.Unlock()
	h.device.scriptCaptures(baselinePageStep(pageA))
	o3 := baselineObserve(t, h, "PgenerationAfter", o2.ID)
	requireFullTree(t, o3, 3)

	h.engine.mu.Lock()
	activeID := h.engine.activeBaselineID
	pool := append([]*baselineState(nil), h.engine.baselines...)
	h.engine.mu.Unlock()
	if activeID != "" {
		t.Fatalf("active baseline = %q after display generation change", activeID)
	}
	for _, entry := range pool {
		if entry != nil && entry.DisplayGeneration != "44444444-4444-4444-8444-444444444444" {
			t.Fatalf("old-generation baseline survived: %+v", entry)
		}
	}
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{Reference: o1.ID}); apiErr != nil {
		t.Fatalf("inspect old baseline: %v", apiErr)
	}
}

// TestBaselinePoolRejectedPublicationKeepsCurrentBase proves a rejected
// publication never prunes the baseline that is still the base of the current
// observation, even when that entry is already outside the next revision
// window, and that a later certain commit prunes it while the observation stays
// readable.
func TestBaselinePoolRejectedPublicationKeepsCurrentBase(t *testing.T) {
	h := newHarness(t)
	pageA := baselineRows("row", 8)
	pageB := baselineRows("beta", 3)
	h.device.scriptCaptures(baselinePageStep(pageA))
	start := h.startSession()
	o1 := start.Observation

	h.device.scriptCaptures(baselinePageStep(pageB))
	o2 := baselineObserve(t, h, "PrejectBaseB", o1.ID)
	requireFullTree(t, o2, 2)
	h.device.scriptCaptures(baselinePageStep(pageA))
	o3 := baselineObserve(t, h, "PrejectBack", o2.ID)
	requireBaselineDelta(t, o3, 3, o1.ID)
	h.device.scriptCaptures(baselinePageStep(pageA))
	o4 := baselineObserve(t, h, "PrejectSame", o3.ID)
	requireBaselineDelta(t, o4, 4, o1.ID)
	h.device.scriptCaptures(baselinePageStep(pageA))
	o5 := baselineObserve(t, h, "PrejectSpanFour", o4.ID)
	requireBaselineDelta(t, o5, 5, o1.ID)

	before := fingerprintEngineHistory(h.engine)
	h.engine.cfg.MaxRetainedSnapshotCostBytes = 1
	h.device.scriptCaptures(baselinePageStep(pageA))
	rejected, apiErr := h.engine.Observe(context.Background(), "agent", "PrejectBudget",
		&protocol.ObserveRequest{Reference: o5.ID})
	if apiErr != nil {
		t.Fatalf("observe rejected at admission: %v", apiErr)
	}
	if rejected.Observation == nil || rejected.Observation.Error == nil ||
		rejected.Observation.Error.Code != protocol.CodeTreeTooLarge {
		t.Fatalf("expected tree_too_large, got %+v", rejected.Observation)
	}
	after := fingerprintEngineHistory(h.engine)
	if !sameBaselinePool(after.baselines, before.baselines) ||
		after.activeBaselineID != before.activeBaselineID {
		t.Fatalf("rejected publication pruned the current base: %+v -> %+v",
			before.baselines, after.baselines)
	}
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{Reference: o1.ID}); apiErr != nil {
		t.Fatalf("inspect current base after rejection: %v", apiErr)
	}

	// A certain commit for revision 6 does prune the out-of-window entry; the
	// retained observation itself stays readable.
	h.engine.cfg = DefaultConfig(h.engine.cfg.ServiceInstanceID, h.engine.cfg.AgentName)
	h.device.scriptCaptures(baselinePageStep(pageA))
	o6 := baselineObserve(t, h, "PrejectCommitted", o5.ID)
	requireFullTree(t, o6, 6)
	h.engine.mu.Lock()
	pruned := true
	for _, entry := range h.engine.baselines {
		if entry != nil && entry.ObservationID == o1.ID {
			pruned = false
		}
	}
	activeID := h.engine.activeBaselineID
	h.engine.mu.Unlock()
	if !pruned || activeID != "" {
		t.Fatalf("out-of-window baseline survived a certain commit: active=%q", activeID)
	}
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{Reference: o1.ID}); apiErr != nil {
		t.Fatalf("inspect pruned-but-retained observation: %v", apiErr)
	}
}

// TestBaselinePoolLogsSelectedPath proves the closed baseline_pool enum is
// recorded for every published observation: full, primary and historical.
func TestBaselinePoolLogsSelectedPath(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	pageA := baselineRows("row", 10)
	pageB := baselineRows("beta", 4)
	h.device.scriptCaptures(baselinePageStep(pageA))
	start := h.startSession()
	o1 := start.Observation

	h.device.scriptCaptures(baselinePageStep(pageB))
	o2 := baselineObserve(t, h, "PlogFull", o1.ID)
	// Confirming page B makes the page A return a historical probe.
	h.device.scriptCaptures(baselinePageStep(pageA))
	o3 := baselineObserve(t, h, "PlogHistorical", o2.ID)
	// The historical page A base is now the active baseline, so the same page
	// keeps taking the primary path.
	h.device.scriptCaptures(baselinePageStep(pageA))
	baselineObserve(t, h, "PlogPrimary", o3.ID)
	logger.Close()

	recorded := buffer.String()
	requireBaselineLogLine(t, recorded, "PlogFull", "full", "full")
	requireBaselineLogLine(t, recorded, "PlogHistorical", "historical", "delta")
	requireBaselineLogLine(t, recorded, "PlogPrimary", "primary", "delta")
}

func requireBaselineLogLine(t *testing.T, recorded, operationID, pool, format string) {
	t.Helper()
	want := `observation.baseline service_instance="instance-test"` + ` operation_id="` + operationID +
		`" baseline_pool="` + pool + `" format="` + format + `"`
	if !strings.Contains(recorded, want) {
		t.Fatalf("event log has no baseline line %q:\n%s", want, recorded)
	}
}

// TestBaselinePoolCleanupClearsPool proves the session lifecycle reset: a
// completed cleanup clears the pool and the active alignment without touching
// any other authority.
func TestBaselinePoolCleanupClearsPool(t *testing.T) {
	h := newHarness(t)
	pageA := baselineRows("row", 6)
	h.device.scriptCaptures(baselinePageStep(pageA))
	start := h.startSession()
	o1 := start.Observation
	h.device.scriptCaptures(baselinePageStep(pageA))
	baselineObserve(t, h, "PlifecycleBaseline", o1.ID)
	h.engine.mu.Lock()
	pooled := len(h.engine.baselines)
	h.engine.mu.Unlock()
	if pooled == 0 {
		t.Fatal("fixture confirmed no baseline")
	}

	if _, apiErr := h.engine.End("agent", "", start.OperationID); apiErr != nil {
		t.Fatalf("end rejected: %v", apiErr)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.sync(nil, nil)
		h.engine.mu.Lock()
		complete := h.engine.cleanup != nil && h.engine.cleanup.State == protocol.CleanupComplete
		h.engine.mu.Unlock()
		if complete {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.engine.mu.Lock()
	cleanup := h.engine.cleanup
	remaining := len(h.engine.baselines)
	activeID := h.engine.activeBaselineID
	h.engine.mu.Unlock()
	if cleanup == nil || cleanup.State != protocol.CleanupComplete {
		t.Fatalf("cleanup did not complete: %+v", cleanup)
	}
	if remaining != 0 || activeID != "" {
		t.Fatalf("cleanup kept %d baselines (active %q)", remaining, activeID)
	}
}

// candidateAgainstCounter counts historical page-return probes.
type candidateAgainstCounter struct {
	Projector
	calls int
}

func (p *candidateAgainstCounter) CandidateAgainst(prev *Projection,
	capture *device.Capture) (*Projection, func(), error) {
	p.calls++
	return p.Projector.CandidateAgainst(prev, capture)
}

// TestBaselinePoolPrimaryPathSkipsHistoricalProbe proves the direct main path:
// a primary delta below 70% never enters the historical probe, while a page
// return that the primary cannot anchor does probe.
func TestBaselinePoolPrimaryPathSkipsHistoricalProbe(t *testing.T) {
	h := newHarness(t)
	counter := &candidateAgainstCounter{Projector: h.engine.projector}
	h.engine.projector = counter
	pageA := baselineRows("row", 8)
	pageB := baselineRows("beta", 3)
	h.device.scriptCaptures(baselinePageStep(pageA))
	start := h.startSession()
	o1 := start.Observation

	h.device.scriptCaptures(baselinePageStep(pageA))
	o2 := baselineObserve(t, h, "PshortPrimary", o1.ID)
	requireBaselineDelta(t, o2, 2, o1.ID)
	if counter.calls != 0 {
		t.Fatalf("primary delta probed %d historical baselines", counter.calls)
	}

	h.device.scriptCaptures(baselinePageStep(pageB))
	o3 := baselineObserve(t, h, "PshortFull", o2.ID)
	requireFullTree(t, o3, 3)
	if counter.calls != 0 {
		t.Fatalf("full fallback probed %d historical baselines", counter.calls)
	}

	h.device.scriptCaptures(baselinePageStep(pageA))
	o4 := baselineObserve(t, h, "PshortHistorical", o3.ID)
	requireBaselineDelta(t, o4, 4, o1.ID)
	if counter.calls != 1 {
		t.Fatalf("historical page return probed %d baselines, want 1", counter.calls)
	}
}

// TestBaselinePoolAgeExpiryFallsBackToFull proves the age boundary: a baseline
// at or past maxBaselineAge no longer backs a delta.
func TestBaselinePoolAgeExpiryFallsBackToFull(t *testing.T) {
	h := newHarness(t)
	pageA := baselineRows("row", 6)
	h.device.scriptCaptures(baselinePageStep(pageA))
	start := h.startSession()
	o1 := start.Observation
	h.device.scriptCaptures(baselinePageStep(pageA))
	o2 := baselineObserve(t, h, "PageBaseline", o1.ID)
	requireBaselineDelta(t, o2, 2, o1.ID)

	h.engine.mu.Lock()
	for _, entry := range h.engine.baselines {
		entry.CapturedAt = time.Now().Add(-maxBaselineAge - time.Second)
	}
	h.engine.mu.Unlock()
	h.device.scriptCaptures(baselinePageStep(pageA))
	o3 := baselineObserve(t, h, "PageExpired", o2.ID)
	requireFullTree(t, o3, 3)
}

// TestBaselinePoolAdmitsOnlyConfirmedFull proves the single confirmation
// entry: a delta observation never enters the pool, and a full observation does.
func TestBaselinePoolAdmitsOnlyConfirmedFull(t *testing.T) {
	h := newHarness(t)
	pageA := baselineRows("row", 6)
	h.device.scriptCaptures(baselinePageStep(pageA))
	start := h.startSession()
	o1 := start.Observation
	h.device.scriptCaptures(baselinePageStep(pageA))
	o2 := baselineObserve(t, h, "PonlyFullDelta", o1.ID)
	requireBaselineDelta(t, o2, 2, o1.ID)

	h.engine.mu.Lock()
	before := len(h.engine.baselines)
	h.engine.confirmBaselineLocked(h.engine.current)
	afterDelta := len(h.engine.baselines)
	h.engine.mu.Unlock()
	if afterDelta != before {
		t.Fatalf("delta observation entered the pool: %d -> %d", before, afterDelta)
	}

	h.device.scriptCaptures(baselinePageStep(pageA))
	forced, apiErr := h.engine.Observe(context.Background(), "agent", "PonlyFullFull",
		&protocol.ObserveRequest{Reference: o2.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("forced full observe rejected: %v", apiErr)
	}
	o3 := forced.Observation
	requireFullTree(t, o3, 3)
	h.engine.mu.Lock()
	h.engine.confirmBaselineLocked(h.engine.current)
	pooled := false
	for _, entry := range h.engine.baselines {
		if entry != nil && entry.ObservationID == o3.ID {
			pooled = true
		}
	}
	h.engine.mu.Unlock()
	if !pooled {
		t.Fatal("confirmed full observation did not enter the pool")
	}
}
