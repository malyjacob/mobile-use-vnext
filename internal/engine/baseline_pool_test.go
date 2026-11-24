package engine

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
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
	return baselineObserveRequest(t, h, p, &protocol.ObserveRequest{Reference: reference})
}

func baselineObserveRequest(t *testing.T, h *harness, p string,
	request *protocol.ObserveRequest) *protocol.Observation {
	t.Helper()
	result, apiErr := h.engine.Observe(context.Background(), "agent", p, request)
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

func publicNodeByLabel(root *protocol.Node, label string) *protocol.Node {
	if root == nil {
		return nil
	}
	for _, value := range root.Label {
		if value == label {
			return root
		}
	}
	for _, child := range root.Children {
		if found := publicNodeByLabel(child, label); found != nil {
			return found
		}
	}
	return nil
}

// TestBaselinePoolPageReturnWindow covers the split confirmed-full windows: a
// backup page return inside ten revisions becomes current, then that same full
// remains available through the twelve-revision primary span. Span thirteen
// falls back to full.
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

	last := o5
	for revision := 6; revision <= 13; revision++ {
		h.device.scriptCaptures(baselinePageStep(pageAChanged))
		last = baselineObserve(t, h, fmt.Sprintf("PbaselineCurrentSpan%d", revision), last.ID)
		requireBaselineDelta(t, last, revision, o1.ID)
	}

	// Span thirteen is outside the extended current window; the pooled page B
	// backup (span twelve) is outside the backup window too.
	h.device.scriptCaptures(baselinePageStep(pageA))
	o14 := baselineObserve(t, h, "PbaselineSpanThirteen", last.ID)
	requireFullTree(t, o14, 14)

	// The discarded probes never shifted the ref high-water mark: after the new
	// full is confirmed, the same page is again an empty delta.
	h.device.scriptCaptures(baselinePageStep(pageA))
	o15 := baselineObserve(t, h, "PbaselineAfterFull", o14.ID)
	after := requireBaselineDelta(t, o15, 15, o14.ID)
	if !after.IsEmpty() {
		t.Fatalf("post-full continuity was polluted: %+v", after)
	}
}

// TestBaselinePoolDemotedPrimaryReselectsAtBackupWindow follows the proposal's
// baseline timeline: a primary used through span five is demoted to a backup at
// span six, stays recoverable through the ten-revision backup window, is selected
// back at span ten, and then keeps the primary window through span twelve before
// the thirteen-span fallback.
func TestBaselinePoolDemotedPrimaryReselectsAtBackupWindow(t *testing.T) {
	h := newHarness(t)
	pageA := baselineRows("alpha", 8)
	pageAChanged := append(append([]rawNodeSpec(nil), pageA...), rawNodeSpec{
		Parent: 0,
		Bounds: device.Rect{Left: 40, Top: 1600, Right: 1040, Bottom: 1700},
		Class:  "android.widget.TextView",
		Text:   "alpha-extra",
	})
	pageB := baselineRows("beta", 4)

	h.device.scriptCaptures(baselinePageStep(pageA))
	start := h.startSession()
	o1 := start.Observation
	requireFullTree(t, o1, 1)

	// Spans one through five stay on the primary baseline.
	last := o1
	for revision := 2; revision <= 6; revision++ {
		h.device.scriptCaptures(baselinePageStep(pageAChanged))
		last = baselineObserve(t, h, fmt.Sprintf("PdemotedPrimary%d", revision), last.ID)
		requireBaselineDelta(t, last, revision, o1.ID)
	}

	// Publishing page B at span six demotes the page A full to a backup. It must
	// not be pruned immediately, unlike the old four-revision backup window.
	h.device.scriptCaptures(baselinePageStep(pageB))
	pageBFull := baselineObserve(t, h, "PdemotedPageB", last.ID)
	requireFullTree(t, pageBFull, 7)
	h.engine.mu.Lock()
	pooled := baselineWithID(h.engine.baselines, o1.ID) != nil
	demotedSpan := pageBFull.Revision - o1.Revision
	h.engine.mu.Unlock()
	if !pooled || demotedSpan != 6 {
		t.Fatalf("page A baseline pooled=%t span=%d, want a span-six backup",
			pooled, demotedSpan)
	}

	// Hold page B through span ten so the demoted page A full reaches the backup
	// window boundary; the newest page B full keeps its own deltas.
	last = pageBFull
	for revision := 8; revision <= 10; revision++ {
		h.device.scriptCaptures(baselinePageStep(pageB))
		last = baselineObserve(t, h, fmt.Sprintf("PdemotedKeepPageB%d", revision), last.ID)
		requireBaselineDelta(t, last, revision, pageBFull.ID)
	}

	// The page A return at span ten is the last selectable backup revision. The
	// selected backup becomes current, so the primary window applies after.
	h.device.scriptCaptures(baselinePageStep(pageA))
	reselected := baselineObserve(t, h, "PdemotedReselectAtSpanTen", last.ID)
	requireBaselineDelta(t, reselected, 11, o1.ID)
	if span := reselected.Revision - o1.Revision; span != backupBaselineRevisionSpan {
		t.Fatalf("reselect span = %d, want the backup boundary %d",
			span, backupBaselineRevisionSpan)
	}
	h.engine.mu.Lock()
	activeID := h.engine.activeBaselineID
	h.engine.mu.Unlock()
	if activeID != o1.ID {
		t.Fatalf("active baseline = %q after reselect, want %q", activeID, o1.ID)
	}

	// The reselected baseline keeps the primary window through span twelve.
	last = reselected
	for revision := 12; revision <= 13; revision++ {
		h.device.scriptCaptures(baselinePageStep(pageAChanged))
		last = baselineObserve(t, h, fmt.Sprintf("PdemotedPrimaryAgain%d", revision), last.ID)
		requireBaselineDelta(t, last, revision, o1.ID)
	}

	// Span thirteen expires the reselected primary.
	h.device.scriptCaptures(baselinePageStep(pageAChanged))
	expired := baselineObserve(t, h, "PdemotedSpanThirteen", last.ID)
	requireFullTree(t, expired, 14)
}

// TestBaselinePoolAllPoolMayReuseBannerRef pins the selected shortest-delta
// behavior: when the banner returns, its original full is the shortest match and
// the old banner ref is intentionally reused.
func TestBaselinePoolAllPoolMayReuseBannerRef(t *testing.T) {
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
	if !back.IsEmpty() {
		t.Fatalf("banner return did not select the original full: %+v", back)
	}
	reused := publicNodeByLabel(currentTree(t, h.engine).Root, "temporary-banner")
	if reused == nil || reused.Ref != banner.Ref {
		t.Fatalf("banner ref = %+v, want reused %q", reused, banner.Ref)
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
	// retained observation itself stays readable. The current twelve-revision
	// window keeps this base through revision thirteen.
	h.engine.cfg = DefaultConfig(h.engine.cfg.ServiceInstanceID, h.engine.cfg.AgentName)
	last := o5
	for revision := 6; revision <= 13; revision++ {
		h.device.scriptCaptures(baselinePageStep(pageA))
		last = baselineObserve(t, h, fmt.Sprintf("PrejectCommitted%d", revision), last.ID)
		requireBaselineDelta(t, last, revision, o1.ID)
	}
	h.device.scriptCaptures(baselinePageStep(pageA))
	o14 := baselineObserve(t, h, "PrejectWindowExpired", last.ID)
	requireFullTree(t, o14, 14)
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

type blockingCandidateAgainstProjector struct {
	Projector
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingCandidateAgainstProjector) CandidateAgainst(prev *Projection,
	capture *device.Capture) (*Projection, func(), error) {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return p.Projector.CandidateAgainst(prev, capture)
}

// TestBaselinePoolProbesEveryEligibleFull proves the unified path: even a
// qualifying current delta does not suppress matching the other pooled full.
func TestBaselinePoolProbesEveryEligibleFull(t *testing.T) {
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
	if counter.calls != 1 {
		t.Fatalf("one-entry pool probed %d baselines, want 1", counter.calls)
	}

	h.device.scriptCaptures(baselinePageStep(pageB))
	o3 := baselineObserve(t, h, "PshortFull", o2.ID)
	requireFullTree(t, o3, 3)
	h.engine.mu.Lock()
	h.engine.confirmBaselineLocked(h.engine.current)
	poolSize := len(h.engine.baselines)
	activeID := h.engine.activeBaselineID
	h.engine.mu.Unlock()
	if poolSize != 2 || activeID != o3.ID {
		t.Fatalf("confirmed full left pool=%d active=%q, want two entries and %q",
			poolSize, activeID, o3.ID)
	}
	if counter.calls != 2 {
		t.Fatalf("full fallback probed %d baselines, want 2", counter.calls)
	}

	h.device.scriptCaptures(baselinePageStep(pageA))
	o4 := baselineObserve(t, h, "PshortHistorical", o3.ID)
	requireBaselineDelta(t, o4, 4, o1.ID)
	if counter.calls != 4 {
		t.Fatalf("two-entry page return probed %d baselines, want 4", counter.calls)
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

func TestBaselinePoolSplitRevisionWindows(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()

	h.engine.mu.Lock()
	session := h.engine.session
	tree := start.Observation.Tree.Full
	currentID := "Owindow_current"
	backupID := "Owindow_backup"
	common := func(id string, revision int) *baselineState {
		return &baselineState{
			ObservationID: id, Origin: session.Origin, Revision: revision,
			CapturedAt: time.Now(), DisplayID: session.Display.ID,
			DisplayGeneration: session.Display.Generation, Tree: tree,
		}
	}
	h.engine.baselines = []*baselineState{
		common(currentID, session.Revision+3),
		common(backupID, session.Revision+5),
	}
	nextRevision := session.Revision + 15
	pool := h.engine.baselinePoolForActiveLocked(nextRevision, currentID, time.Now())
	h.engine.mu.Unlock()
	if baselineWithID(pool, currentID) == nil || baselineWithID(pool, backupID) == nil {
		t.Fatalf("exact twelve/ten windows excluded a baseline: %+v", pool)
	}

	h.engine.mu.Lock()
	nextRevision++
	pool = h.engine.baselinePoolForActiveLocked(nextRevision, currentID, time.Now())
	withoutCurrent := h.engine.baselinePoolForActiveLocked(nextRevision, "", time.Now())
	h.engine.mu.Unlock()
	if len(pool) != 0 {
		t.Fatalf("span thirteen/eleven kept baselines: %+v", pool)
	}
	if len(withoutCurrent) != 0 {
		t.Fatalf("an eleven-span full remained a backup: %+v", withoutCurrent)
	}
}

func TestBaselinePoolPostSelectionPrunesOldCurrent(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.engine.mu.Lock()
	session := h.engine.session
	tree := start.Observation.Tree.Full
	oldCurrent := &baselineState{
		ObservationID: "Oold_current", Origin: session.Origin,
		Revision: session.Revision + 1, CapturedAt: time.Now(),
		DisplayID: session.Display.ID, DisplayGeneration: session.Display.Generation,
		Tree: tree,
	}
	selected := &baselineState{
		ObservationID: "Oselected", Origin: session.Origin,
		Revision: session.Revision + 6, CapturedAt: time.Now(),
		DisplayID: session.Display.ID, DisplayGeneration: session.Display.Generation,
		Tree: tree,
	}
	h.engine.baselines = []*baselineState{selected, oldCurrent}
	// The old current still serves as the active twelve-span baseline before the
	// selection, but it is a twelve-span backup afterwards and falls outside the
	// ten-revision backup window.
	nextRevision := session.Revision + 13
	before := h.engine.baselinePoolForActiveLocked(nextRevision,
		oldCurrent.ObservationID, time.Now())
	after := h.engine.baselinePoolForActiveLocked(nextRevision,
		selected.ObservationID, time.Now())
	h.engine.mu.Unlock()
	if baselineWithID(before, oldCurrent.ObservationID) == nil ||
		baselineWithID(before, selected.ObservationID) == nil {
		t.Fatalf("selection pool omitted current or backup: %+v", before)
	}
	if len(after) != 1 || after[0].ObservationID != selected.ObservationID {
		t.Fatalf("post-selection pool = %+v, want only %q", after, selected.ObservationID)
	}
}

func TestBaselinePoolKeepsOneCurrentAndFourBackups(t *testing.T) {
	h := newHarness(t)
	h.engine.mu.Lock()
	h.engine.baselines = []*baselineState{
		{ObservationID: "Ob5", Revision: 5},
		{ObservationID: "Ob4", Revision: 4},
		{ObservationID: "Ob3", Revision: 3},
		{ObservationID: "Ob2", Revision: 2},
		{ObservationID: "Ob1", Revision: 1},
	}
	h.engine.activeBaselineID = ""
	h.engine.truncateBaselinesLocked()
	if len(h.engine.baselines) != maxBackupBaselines {
		t.Fatalf("backup-only pool length = %d, want %d",
			len(h.engine.baselines), maxBackupBaselines)
	}
	if maxBackupBaselines != 4 {
		t.Fatalf("backup-only capacity = %d, want 4", maxBackupBaselines)
	}
	h.engine.baselines = []*baselineState{
		{ObservationID: "Ob5", Revision: 5},
		{ObservationID: "Ob4", Revision: 4},
		{ObservationID: "Ob3", Revision: 3},
		{ObservationID: "Ob2", Revision: 2},
		{ObservationID: "Ob1", Revision: 1},
	}
	h.engine.activeBaselineID = "Ob5"
	h.engine.truncateBaselinesLocked()
	length := len(h.engine.baselines)
	h.engine.mu.Unlock()
	if length != maxBaselines {
		t.Fatalf("current plus backups pool length = %d, want %d", length, maxBaselines)
	}
	if maxBaselines != 5 {
		t.Fatalf("full pool capacity = %d, want 5", maxBaselines)
	}
}

func TestBaselineDeltaCandidateTieOrder(t *testing.T) {
	candidates := []baselineDeltaCandidate{
		{baseline: &baselineState{ObservationID: "Onew", Revision: 30}, deltaBytes: 5},
		{baseline: &baselineState{ObservationID: "Ocurrent", Revision: 1},
			deltaBytes: 5, current: true},
		{baseline: &baselineState{ObservationID: "Oshort", Revision: 2}, deltaBytes: 4},
	}
	sortBaselineDeltaCandidates(candidates)
	got := []string{
		candidates[0].baseline.ObservationID,
		candidates[1].baseline.ObservationID,
		candidates[2].baseline.ObservationID,
	}
	want := []string{"Oshort", "Ocurrent", "Onew"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("candidate order = %v, want %v", got, want)
		}
	}
}

func TestBaselinePoolForceFullBypassesBaselineMatching(t *testing.T) {
	h := newHarness(t)
	counter := &candidateAgainstCounter{Projector: h.engine.projector}
	h.engine.projector = counter
	h.device.scriptCaptures(baselinePageStep(baselineRows("row", 6)))
	start := h.startSession()
	delta := baselineObserve(t, h, "PbypassDelta", start.Observation.ID)
	requireBaselineDelta(t, delta, 2, start.Observation.ID)
	before := counter.calls

	h.device.scriptCaptures(baselinePageStep(baselineRows("row", 6)))
	forced, apiErr := h.engine.Observe(context.Background(), "agent", "PbypassP",
		&protocol.ObserveRequest{Reference: start.OperationID})
	if apiErr != nil {
		t.Fatalf("observe(P) rejected: %v", apiErr)
	}
	requireFullTree(t, forced.Observation, 3)
	if counter.calls != before {
		t.Fatalf("force-full path matched %d baselines", counter.calls-before)
	}
}

func TestBaselinePoolStopDuringMatchingPublishesNothing(t *testing.T) {
	h := newHarness(t)
	page := baselineRows("row", 6)
	h.device.scriptCaptures(baselinePageStep(page))
	start := h.startSession()
	h.device.scriptCaptures(baselinePageStep(page))
	ack := baselineObserve(t, h, "PstopBaselineAck", start.Observation.ID)
	requireBaselineDelta(t, ack, 2, start.Observation.ID)

	blocking := &blockingCandidateAgainstProjector{
		Projector: h.engine.projector,
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	h.engine.projector = blocking
	h.engine.mu.Lock()
	nextBefore := h.engine.nextObservation
	h.engine.mu.Unlock()

	done := make(chan callResult, 1)
	go func() {
		result, apiErr := h.engine.Observe(context.Background(), "agent", "PstopBaseline",
			&protocol.ObserveRequest{Reference: ack.ID})
		done <- callResult{result, apiErr}
	}()
	awaitSignal(t, blocking.entered)
	stopNow(t, h)
	close(blocking.release)
	result := awaitCall(t, done)
	if result.Observation != nil && result.Observation.ID != "" {
		t.Fatalf("STOP published a late observation: %+v", result.Observation)
	}
	h.engine.mu.Lock()
	nextAfter := h.engine.nextObservation
	h.engine.mu.Unlock()
	if nextAfter != nextBefore {
		t.Fatalf("STOP path consumed an observation ID: %d -> %d", nextBefore, nextAfter)
	}
}

func TestBaselinePoolExpiredDuringMatchingFallsBackToFull(t *testing.T) {
	h := newHarness(t)
	page := baselineRows("row", 6)
	h.device.scriptCaptures(baselinePageStep(page))
	start := h.startSession()
	h.device.scriptCaptures(baselinePageStep(page))
	ack := baselineObserve(t, h, "PexpireBaselineAck", start.Observation.ID)
	requireBaselineDelta(t, ack, 2, start.Observation.ID)

	blocking := &blockingCandidateAgainstProjector{
		Projector: h.engine.projector,
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	h.engine.projector = blocking
	done := make(chan callResult, 1)
	go func() {
		result, apiErr := h.engine.Observe(context.Background(), "agent", "PexpireBaseline",
			&protocol.ObserveRequest{Reference: ack.ID})
		done <- callResult{result, apiErr}
	}()
	awaitSignal(t, blocking.entered)
	h.engine.mu.Lock()
	for _, entry := range h.engine.baselines {
		entry.CapturedAt = time.Now().Add(-maxBaselineAge - time.Second)
	}
	h.engine.mu.Unlock()
	close(blocking.release)
	result := awaitCall(t, done)
	requireFullTree(t, result.Observation, 3)
}

func TestBaselinePoolExpiredDuringRetentionPlanningTriesNextCandidate(t *testing.T) {
	h := newHarness(t)
	page := baselineRows("row", 6)
	h.device.scriptCaptures(baselinePageStep(page))
	start := h.startSession()

	h.device.scriptCaptures(baselinePageStep(page))
	secondFull := baselineObserveRequest(t, h, "PpostPlanSecondFull",
		&protocol.ObserveRequest{Reference: start.Observation.ID, Full: true})
	h.device.scriptCaptures(baselinePageStep(page))
	delta := baselineObserve(t, h, "PpostPlanDelta", secondFull.ID)

	h.engine.mu.Lock()
	session := h.engine.session
	firstObs := h.engine.observations[start.Observation.ID]
	secondObs := h.engine.observations[secondFull.ID]
	if session == nil || firstObs == nil || secondObs == nil {
		h.engine.mu.Unlock()
		t.Fatal("post-plan fixture observations are missing")
	}
	nextRevision := session.Revision + 1
	first := &baselineState{
		ObservationID: firstObs.ID, Origin: session.Origin, Revision: firstObs.Revision,
		CapturedAt: time.Now(), DisplayID: session.Display.ID,
		DisplayGeneration: session.Display.Generation,
		Tree:              firstObs.Compact, Projection: firstObs.Projection,
	}
	second := &baselineState{
		ObservationID: secondObs.ID, Origin: session.Origin, Revision: secondObs.Revision,
		CapturedAt: time.Now(), DisplayID: session.Display.ID,
		DisplayGeneration: session.Display.Generation,
		Tree:              secondObs.Compact, Projection: secondObs.Projection,
	}
	h.engine.baselines = []*baselineState{first, second}
	h.engine.activeBaselineID = first.ObservationID
	h.engine.mu.Unlock()

	original := planPublicationRetention
	t.Cleanup(func() { planPublicationRetention = original })
	expired := false
	planPublicationRetention = func(e *Engine, cost int, pool []*baselineState,
		selectedBaseID string) ([]*observationState, []*baselineState, bool) {
		victims, finalPool, fits := original(e, cost, pool, selectedBaseID)
		if !expired && selectedBaseID == first.ObservationID {
			first.CapturedAt = time.Now().Add(-maxBaselineAge - time.Second)
			expired = true
		}
		return victims, finalPool, fits
	}

	h.device.scriptCaptures(baselinePageStep(page))
	selected := baselineObserve(t, h, "PpostPlanExpiry", delta.ID)
	requireBaselineDelta(t, selected, nextRevision, second.ObservationID)
	if !expired {
		t.Fatal("retention planning never exercised the first baseline")
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if baselineWithID(h.engine.baselines, first.ObservationID) != nil {
		t.Fatalf("expired baseline survived the committed pool: %+v", h.engine.baselines)
	}
	if h.engine.activeBaselineID != second.ObservationID {
		t.Fatalf("active baseline = %q, want %q",
			h.engine.activeBaselineID, second.ObservationID)
	}
}

func TestBaselinePoolRechecksAfterRetentionPlanning(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		h.device.scriptCaptures(replayCurrentCapture(t, h.engine))
		installPostPlanMutation(t, func(e *Engine) { e.work.Cancel() })

		result, apiErr := h.engine.Observe(context.Background(), "agent", "PpostPlanCancel",
			&protocol.ObserveRequest{Reference: start.OperationID})
		requirePostPlanRejection(t, result, apiErr, protocol.CodeStopped)
	})

	t.Run("work deadline", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		h.device.scriptCaptures(replayCurrentCapture(t, h.engine))
		installPostPlanMutation(t, func(e *Engine) {
			e.work.Deadline = time.Now().Add(-time.Second)
		})

		result, apiErr := h.engine.Observe(context.Background(), "agent", "PpostPlanDeadline",
			&protocol.ObserveRequest{Reference: start.OperationID})
		requirePostPlanRejection(t, result, apiErr, protocol.CodeStopped)
	})

	t.Run("capture age", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		h.device.scriptCaptures(replayCurrentCapture(t, h.engine))
		installPostPlanMutation(t, func(e *Engine) {
			e.cfg.CaptureMaxAge = -time.Nanosecond
		})

		result, apiErr := h.engine.Observe(context.Background(), "agent", "PpostPlanAge",
			&protocol.ObserveRequest{Reference: start.OperationID})
		requirePostPlanRejection(t, result, apiErr, protocol.CodeStaleScreen)
	})
}

func installPostPlanMutation(t *testing.T, mutate func(*Engine)) {
	t.Helper()
	original := planPublicationRetention
	t.Cleanup(func() { planPublicationRetention = original })
	mutated := false
	planPublicationRetention = func(e *Engine, cost int, pool []*baselineState,
		selectedBaseID string) ([]*observationState, []*baselineState, bool) {
		victims, finalPool, fits := original(e, cost, pool, selectedBaseID)
		if !mutated {
			mutate(e)
			mutated = true
		}
		return victims, finalPool, fits
	}
}

func requirePostPlanRejection(t *testing.T, result *protocol.Result, apiErr *protocol.APIError,
	code string) {
	t.Helper()
	if apiErr != nil {
		t.Fatalf("post-plan rejection was fail-fast instead of a result: %v", apiErr)
	}
	if result == nil || result.Observation == nil || result.Observation.ID != "" ||
		result.Observation.Error == nil || result.Observation.Error.Code != code {
		t.Fatalf("post-plan result = %+v, want failed observation %s", result, code)
	}
}
