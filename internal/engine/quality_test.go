package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// qualityPolicy is a settle policy with no quiet window, so a test exercises
// the comparison and stop rules without paying real dwell time.
func qualityPolicy(minSamples, maxSamples int) SettlePolicy {
	return SettlePolicy{
		Enabled: true, MinSamples: minSamples, MaxSamples: maxSamples,
		MaxBudget: time.Second,
	}
}

func requireQuality(t *testing.T, got *protocol.Quality, state string, samples int, stopReason string) {
	t.Helper()
	if got == nil {
		t.Fatalf("quality is absent, want {%s %d %s}", state, samples, stopReason)
	}
	if got.State != state || got.Samples != samples || got.StopReason != stopReason {
		t.Fatalf("quality = %+v, want {%s %d %s}", *got, state, samples, stopReason)
	}
}

// publishQuality drives one complete acquisition directly, exactly as the
// request paths do, and returns the published observation.
func (h *harness) publishQuality(t *testing.T, work *workState, session *sessionState,
	phase string) *protocol.Observation {
	t.Helper()
	public, obs, apiErr := h.engine.captureAndPublish(work.Ctx, work, session, true, false, phase, nil)
	if apiErr != nil {
		t.Fatalf("%s acquisition rejected: %v", phase, apiErr)
	}
	if public == nil || obs == nil {
		t.Fatalf("%s acquisition returned no observation", phase)
	}
	if obs.Quality == nil {
		t.Fatalf("%s committed observation lost its quality fact", phase)
	}
	return public
}

// slowCapture makes one hierarchy read measurably expensive. The settle budget
// arithmetic compares wall-clock durations, and a fast in-process fake can be
// rounded to zero by the test clock; the delay keeps a budget exhaustion
// deterministic on every host.
func slowCapture(delay time.Duration, step captureStep) captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		time.Sleep(delay)
		return step(f, display)
	}
}

// requireCurrentTreeLabel proves which public tree the engine actually
// committed, independent of the full/delta projection of the result.
func requireCurrentTreeLabel(t *testing.T, h *harness, label string) {
	t.Helper()
	h.engine.mu.Lock()
	current := h.engine.current
	h.engine.mu.Unlock()
	if current == nil || current.Projection == nil ||
		!treeHasLabel(current.Projection.Tree.Root, label) {
		t.Fatalf("published tree lacks label %q", label)
	}
}

// requireQualityLogLine finds one logged event line carrying every wanted
// fragment. It proves the field whitelist kept the controlled keys.
func requireQualityLogLine(t *testing.T, recorded, event string, wants ...string) string {
	t.Helper()
	for _, line := range strings.Split(recorded, "\n") {
		if !strings.Contains(line, event) {
			continue
		}
		all := true
		for _, want := range wants {
			if !strings.Contains(line, want) {
				all = false
				break
			}
		}
		if all {
			return line
		}
	}
	t.Fatalf("event log has no %s line carrying %v: %s", event, wants, recorded)
	return ""
}

// TestQualitySingleFrameBudgetIsUnknown pins the smallest possible run: one
// frame cannot form a comparison, so the state is unknown, not unstable.
func TestQualitySingleFrameBudgetIsUnknown(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Pqualitybudget")
	h.engine.cfg.Settle = SettlePolicies{Act: SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 5, MaxBudget: 5 * time.Millisecond,
	}}
	h.device.scriptCaptures(slowCapture(20*time.Millisecond, markerCapture("A")))

	public := h.publishQuality(t, work, session, "act")
	requireQuality(t, public.Quality, protocol.QualityUnknown, 1, protocol.QualityStopBudget)
	if public.Status != "complete" {
		t.Fatalf("status = %q, want complete", public.Status)
	}
}

// TestQualitySingleFrameUnreadableIsUnknown proves a failed extension read is
// not a sample: the count stays at one and the reason names the read failure.
func TestQualitySingleFrameUnreadableIsUnknown(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Pqualityunreadable")
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 5)}
	h.device.scriptCaptures(markerCapture("A"), failingCapture())

	public := h.publishQuality(t, work, session, "act")
	requireQuality(t, public.Quality, protocol.QualityUnknown, 1, protocol.QualityStopUnreadable)
	if !readTreeHasLabel(public.Tree.Full.Root, "A") {
		t.Fatalf("published frame is not the frame before the failed read")
	}
}

// TestQualityMaxSamplesIsUnstable covers a run that never converges because the
// frame limit is reached.
func TestQualityMaxSamplesIsUnstable(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Pqualitymax")
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 3)}
	h.device.scriptCaptures(markerCapture("A"), markerCapture("B"), markerCapture("C"))

	public := h.publishQuality(t, work, session, "act")
	requireQuality(t, public.Quality, protocol.QualityUnstable, 3, protocol.QualityStopMaxSamples)
	if !readTreeHasLabel(public.Tree.Full.Root, "C") {
		t.Fatalf("published frame is not the newest sampled frame")
	}
}

// TestQualityBudgetExhaustionIsUnstable covers a run with at least two unequal
// frames whose next sample no longer fits the wall-clock budget.
func TestQualityBudgetExhaustionIsUnstable(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Pqualitybudgetunstable")
	h.engine.cfg.Settle = SettlePolicies{Act: SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 5,
		MinDwell: 100 * time.Millisecond, MaxBudget: 150 * time.Millisecond,
	}}
	h.device.scriptCaptures(markerCapture("A"), markerCapture("B"))

	public := h.publishQuality(t, work, session, "act")
	requireQuality(t, public.Quality, protocol.QualityUnstable, 2, protocol.QualityStopBudget)
}

// TestQualityEqualPairIsStable covers the converged case: the later frame of
// the equal pair is published and the reason names the convergence.
func TestQualityEqualPairIsStable(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Pqualitystable")
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 5)}
	h.device.scriptCaptures(markerCapture("A"), markerCapture("B"), markerCapture("B"))

	public := h.publishQuality(t, work, session, "act")
	requireQuality(t, public.Quality, protocol.QualityStable, 3, protocol.QualityStopStable)
	if !readTreeHasLabel(public.Tree.Full.Root, "B") {
		t.Fatalf("published frame is not the later frame of the equal pair")
	}
}

// TestQualityUnreadableAfterTwoFramesIsUnstable proves the failed read neither
// counts as a sample nor hides the unstable verdict of the frames before it.
func TestQualityUnreadableAfterTwoFramesIsUnstable(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Pqualityunreadable2")
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 5)}
	h.device.scriptCaptures(markerCapture("A"), markerCapture("B"), failingCapture())

	public := h.publishQuality(t, work, session, "act")
	requireQuality(t, public.Quality, protocol.QualityUnstable, 2, protocol.QualityStopUnreadable)
	if !readTreeHasLabel(public.Tree.Full.Root, "B") {
		t.Fatalf("published frame is not the last readable frame")
	}
}

// TestQualityObserveIsNotSampled proves observe never settles and says so:
// zero samples and the not_sampled reason. The start observation of the same
// session carries its own settle quality.
func TestQualityObserveIsNotSampled(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	requireQuality(t, start.Observation.Quality, protocol.QualityStable, 2,
		protocol.QualityStopStable)

	result, apiErr := h.engine.Observe(context.Background(), "agent", "Pqualityobserve",
		&protocol.ObserveRequest{Reference: start.Observation.ID})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityUnknown, 0,
		protocol.QualityStopNotSampled)
}

// TestQualityStartUsesStartPolicy proves the first observation reports the
// start tier's own frame limit.
func TestQualityStartUsesStartPolicy(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Start: qualityPolicy(2, 4)}
	h.device.scriptCaptures(
		markerCapture("A"), markerCapture("B"), markerCapture("C"), markerCapture("D"))

	start := h.startSession()
	requireQuality(t, start.Observation.Quality, protocol.QualityUnstable, 4,
		protocol.QualityStopMaxSamples)
}

// TestQualityActPostObservation proves the act post observation reports the act
// tier's convergence.
func TestQualityActPostObservation(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 5)}
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(
		defaultCaptureStep, markerCapture("A"), markerCapture("B"), markerCapture("B"))

	result := h.actWith(t, "Pqualityact", h.tapRefRequest(t, start.Observation.ID, button.Ref))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q, want executed", result.Outcome)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityStable, 3,
		protocol.QualityStopStable)
}

// TestQualityAppPostObservation proves a mutating app branch reports the open
// app tier's convergence on its post observation.
func TestQualityAppPostObservation(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.Settle = SettlePolicies{OpenApp: qualityPolicy(2, 3)}
	first := h.startSession()
	h.device.launchTask = &device.Task{TaskID: 77, Package: "com.example.app"}
	h.device.scriptCaptures(markerCapture("A"), markerCapture("B"), markerCapture("B"))

	result, apiErr := h.engine.App(context.Background(), "agent", "Pqualityapp",
		appOpenRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("app open: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q, want executed", result.Outcome)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityStable, 3,
		protocol.QualityStopStable)
	requireObservationCue(t, result.Observation, "sparse_tree", "", protocol.HintSnap)
}

// TestQualityImageRefreshEqualCompletesPair is the behavior fix of the image
// binding refresh: a run stopped at one frame by the budget becomes stable when
// the refresh frame equals it, and the refresh counts as the second sample.
func TestQualityImageRefreshEqualCompletesPair(t *testing.T) {
	h, _ := imageTestHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Act: SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 5, MaxBudget: 5 * time.Millisecond,
	}}
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	// revalidation, one budget-stopped sampled frame, equal image-binding refresh
	h.device.scriptCaptures(
		defaultCaptureStep, slowCapture(20*time.Millisecond, markerCapture("A")),
		markerCapture("A"))

	result := h.actWith(t, "Pqualityimagestable", tapButtonRequest(t, h, start.Observation.ID))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q, want executed", result.Outcome)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityStable, 2,
		protocol.QualityStopStable)
	requireCurrentTreeLabel(t, h, "A")
}

// TestQualityImageRefreshEqualAfterConvergedRun proves the refresh is counted
// on top of an already converged run: stable with samples+1.
func TestQualityImageRefreshEqualAfterConvergedRun(t *testing.T) {
	h, _ := imageTestHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 5)}
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.scriptCaptures(defaultCaptureStep)

	before := h.device.captureCount()
	result := h.actWith(t, "Pqualityimagestable2", tapButtonRequest(t, h, start.Observation.ID))
	if got := h.device.captureCount() - before; got != 4 {
		t.Fatalf("capture reads = %d, want revalidation + two samples + one refresh", got)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityStable, 3,
		protocol.QualityStopStable)
}

// TestQualityImageRefreshUnequalFinishesRun proves a different refresh frame
// invalidates the earlier pair and the final state comes from the comparisons
// that follow it.
func TestQualityImageRefreshUnequalFinishesRun(t *testing.T) {
	h, _ := imageTestHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 5)}
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.scriptCaptures(
		defaultCaptureStep, markerCapture("A"), markerCapture("A"),
		markerCapture("B"), markerCapture("C"), markerCapture("D"))

	result := h.actWith(t, "Pqualityimageunstable", tapButtonRequest(t, h, start.Observation.ID))
	requireQuality(t, result.Observation.Quality, protocol.QualityUnstable, 5,
		protocol.QualityStopMaxSamples)
	requireCurrentTreeLabel(t, h, "D")
}

// TestQualityImageRefreshUnreadableKeepsRun proves a failed binding refresh
// keeps the old frame and the old quality facts: the failed read is not a
// sample and the image still pairs with the settled frame.
func TestQualityImageRefreshUnreadableKeepsRun(t *testing.T) {
	h, _ := imageTestHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 5)}
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.scriptCaptures(
		defaultCaptureStep, markerCapture("A"), markerCapture("A"), failingCapture())

	result := h.actWith(t, "Pqualityimagereadfail", tapButtonRequest(t, h, start.Observation.ID))
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q, want executed", result.Outcome)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityStable, 2,
		protocol.QualityStopStable)
	requireCurrentTreeLabel(t, h, "A")
	if result.Observation.Image == nil || result.Observation.Image.Error != nil {
		t.Fatalf("image lost from the settled frame observation: %+v", result.Observation.Image)
	}
}

// TestQualityImagePathNeverExceedsSix pins the act-with-image ceiling: the
// five-frame policy plus the single binding refresh, never more.
func TestQualityImagePathNeverExceedsSix(t *testing.T) {
	h, _ := imageTestHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Act: SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 5,
		MaxBudget: 2 * time.Second,
	}}
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.scriptCaptures(
		defaultCaptureStep, markerCapture("A"), markerCapture("B"),
		markerCapture("C"), markerCapture("D"), markerCapture("E"), markerCapture("F"))

	before := h.device.captureCount()
	result := h.actWith(t, "Pqualityimagemax", tapButtonRequest(t, h, start.Observation.ID))
	if got := h.device.captureCount() - before; got != 7 {
		t.Fatalf("capture reads = %d, want revalidation + five samples + one refresh", got)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityUnstable, 6,
		protocol.QualityStopMaxSamples)
	requireCurrentTreeLabel(t, h, "F")
}

// TestQualityStalePairCannotRescueStable proves a pair that converged earlier
// cannot report stable after a later refresh frame differs and the remaining
// budget no longer allows another comparison.
func TestQualityStalePairCannotRescueStable(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Pqualitystale")
	policy := qualityPolicy(2, 5)
	h.device.scriptCaptures(markerCapture("A"), markerCapture("A"))
	run, apiErr := h.engine.settleCapture(work.Ctx, work, session, "act", policy)
	if apiErr != nil {
		t.Fatalf("settle failed: %v", apiErr)
	}
	requireQuality(t, qualityOf(policy, run), protocol.QualityStable, 2,
		protocol.QualityStopStable)

	// The wall clock cost of the earlier pair is exactly what the budget
	// pre-check accounts for; move the run start so the refresh finishes the
	// budget deterministically instead of racing a real timer.
	run.started = time.Now().Add(-time.Hour)
	h.device.scriptCaptures(markerCapture("B"))
	tree, apiErr := h.engine.captureSettledTree(work.Ctx, work, session, "act", policy, run, true)
	if apiErr != nil {
		t.Fatalf("refresh acquisition failed: %v", apiErr)
	}
	requireQuality(t, qualityOf(policy, run), protocol.QualityUnstable, 3,
		protocol.QualityStopBudget)
	if !treeHasLabel(tree.projection.Tree.Root, "B") {
		t.Fatalf("published tree is not the differing refresh frame")
	}
}

// TestQualityFailedTreeHasNoQuality proves a failed observation (no published
// tree) carries no quality at all, on the observe and act post paths.
func TestQualityFailedTreeHasNoQuality(t *testing.T) {
	t.Run("observe", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		h.device.mu.Lock()
		h.device.captureErrs = true
		h.device.mu.Unlock()

		result, apiErr := h.engine.Observe(context.Background(), "agent", "Pqualityfailed",
			&protocol.ObserveRequest{Reference: start.Observation.ID})
		if apiErr != nil {
			t.Fatalf("observe rejected: %v", apiErr)
		}
		if result.Observation == nil || result.Observation.Status != "failed" {
			t.Fatalf("observation = %+v, want failed", result.Observation)
		}
		if result.Observation.Quality != nil {
			t.Fatalf("failed observation carries quality: %+v", result.Observation.Quality)
		}
	})
	t.Run("act post", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		h.engine.cfg.Settle = SettlePolicies{}
		// The live revalidation does not encode a full tree; shrinking the
		// budget makes only the post tree encoding fail.
		h.engine.cfg.MaxFullTreeBytes = 16
		button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
		if button == nil {
			t.Fatal("button not found in tree")
		}

		result := h.actWith(t, "Pqualityactfailed",
			h.tapRefRequest(t, start.Observation.ID, button.Ref))
		if result.Outcome != OutcomeExecuted {
			t.Fatalf("outcome = %q, want executed", result.Outcome)
		}
		if result.Observation == nil || result.Observation.ID != "" {
			t.Fatalf("post failure returned an observation id: %+v", result.Observation)
		}
		if result.Observation.Quality != nil {
			t.Fatalf("failed observation carries quality: %+v", result.Observation.Quality)
		}
	})
}

// TestQualityKeepsObservationFacts proves an unstable observation is published,
// keeps the normal status and actionable window, and stays claimable.
func TestQualityKeepsObservationFacts(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 2)}
	start := h.startSession()
	h.device.scriptCaptures(defaultCaptureStep, markerCapture("A"), markerCapture("B"))
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}

	first := h.actWith(t, "Pqualityfacts1",
		h.tapRefRequest(t, start.Observation.ID, button.Ref))
	if first.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q, want executed", first.Outcome)
	}
	obs := first.Observation
	requireQuality(t, obs.Quality, protocol.QualityUnstable, 2, protocol.QualityStopMaxSamples)
	if obs.Status != "complete" {
		t.Fatalf("status = %q, want complete", obs.Status)
	}
	captured, parseErr := time.Parse(time.RFC3339Nano, obs.CapturedAt)
	if parseErr != nil {
		t.Fatalf("captured_at = %q: %v", obs.CapturedAt, parseErr)
	}
	actionable, parseErr := time.Parse(time.RFC3339Nano, obs.ActionableUntil)
	if parseErr != nil {
		t.Fatalf("actionable_until = %q: %v", obs.ActionableUntil, parseErr)
	}
	if got := actionable.Sub(captured); got != h.engine.cfg.ObservationLifetime {
		t.Fatalf("actionable window = %s, want the unchanged %s",
			got, h.engine.cfg.ObservationLifetime)
	}

	// The unstable observation is still a normal ready O: the next act claims
	// it, dispatches nothing before proving it, and executes against it.
	retryButton := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if retryButton == nil {
		t.Fatal("button not found in the unstable tree")
	}
	h.device.scriptCaptures(markerCapture("B"))
	second := h.actWith(t, "Pqualityfacts2", h.tapRefRequest(t, obs.ID, retryButton.Ref))
	if second.Outcome != OutcomeExecuted {
		t.Fatalf("retry outcome = %q error %+v, want executed", second.Outcome, second.Error)
	}
	requireQuality(t, second.Observation.Quality, protocol.QualityStable, 2,
		protocol.QualityStopStable)
	if len(h.device.taps) != 2 {
		t.Fatalf("dispatched taps = %d, want exactly 2 (one per executed act)", len(h.device.taps))
	}
}

// TestQualityRejectionDeliveryIsNotSampled proves the delivered frame of a
// rejected revalidation carries the fixed not_sampled quality.
func TestQualityRejectionDeliveryIsNotSampled(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	// A same-band vertical move is now re-acquired by Tier 5, so this rejection
	// case uses a target text change: it fails the structure match at every tier.
	h.device.scriptCaptures(markerCapture("OK-changed"))

	result := h.actWith(t, "Pqualitydelivered",
		h.tapRefRequest(t, start.Observation.ID, button.Ref))
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	if result.Observation == nil {
		t.Fatal("rejection carried no delivered observation")
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityUnknown, 0,
		protocol.QualityStopNotSampled)
}

// TestQualityLogsPublishedObservation proves the settle and quality events
// carry the operation id and the actually published observation id through the
// whitelist.
func TestQualityLogsPublishedObservation(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	h.engine.cfg.Settle = SettlePolicies{Act: qualityPolicy(2, 2)}
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(defaultCaptureStep, markerCapture("A"), markerCapture("B"))

	result := h.actWith(t, "Pqualitylog", h.tapRefRequest(t, start.Observation.ID, button.Ref))
	requireQuality(t, result.Observation.Quality, protocol.QualityUnstable, 2,
		protocol.QualityStopMaxSamples)
	logger.Close()
	recorded := buffer.String()

	requireQualityLogLine(t, recorded, "observation.settle",
		`operation_id="Pqualitylog"`, "samples=2", `stop_reason="max_samples"`)
	requireQualityLogLine(t, recorded, "observation.quality",
		`operation_id="Pqualitylog"`, `observation_id="`+result.Observation.ID+`"`,
		`phase="act"`, `state="unstable"`, "samples=2", `stop_reason="max_samples"`)
}

// TestQualityDeliveryLogsOperationID proves the delivered-frame event carries
// the operation id and the quality event names the delivered observation.
func TestQualityDeliveryLogsOperationID(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	// A same-band vertical move is now re-acquired by Tier 5, so this rejection
	// case uses a target text change: it fails the structure match at every tier.
	h.device.scriptCaptures(markerCapture("OK-changed"))

	result := h.actWith(t, "Pqualitydeliverylog",
		h.tapRefRequest(t, start.Observation.ID, button.Ref))
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	logger.Close()
	recorded := buffer.String()

	requireQualityLogLine(t, recorded, "act.delivered",
		`operation_id="Pqualitydeliverylog"`, `state="published"`)
	requireQualityLogLine(t, recorded, "observation.quality",
		`operation_id="Pqualitydeliverylog"`,
		`observation_id="`+result.Observation.ID+`"`,
		`state="unknown"`, "samples=0", `stop_reason="not_sampled"`)
}

// TestQualityImageFailureLogsOperationID proves the image event of a requested
// but missing image carries the operation id through the whitelist.
func TestQualityImageFailureLogsOperationID(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	start := h.startSession()

	result, apiErr := h.engine.Observe(context.Background(), "agent", "Pqualityimagelog",
		&protocol.ObserveRequest{Reference: start.Observation.ID, Image: true})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	if result.Observation.Status != "partial" {
		t.Fatalf("status = %q, want partial", result.Observation.Status)
	}
	logger.Close()
	requireQualityLogLine(t, buffer.String(), "observation.image",
		`operation_id="Pqualityimagelog"`)
}
