package engine

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/imaging"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// fastWaitPolicy shrinks the fixed sampling policy so a test can exercise the
// real loop instead of waiting for production dwell times.
func fastWaitPolicy() WaitPolicy {
	return WaitPolicy{
		MinDwell:          2 * time.Millisecond,
		StableFrames:      3,
		MaxSamples:        80,
		ProbeOnFailStreak: 3,
		FrameTimeout:      16 * time.Second,
	}
}

// waitForTestHarness starts one session and returns its first observation id.
func waitForTestHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.engine.cfg.Wait = fastWaitPolicy()
	start := h.startSession()
	return h, start.Observation.ID
}

func TestWaitPolicyValidate(t *testing.T) {
	if err := DefaultWaitPolicy().Validate(); err != nil {
		t.Fatalf("default wait policy rejected: %v", err)
	}
	cases := []struct {
		name   string
		policy WaitPolicy
	}{
		{"negative dwell", WaitPolicy{MinDwell: -1, StableFrames: 3, MaxSamples: 8, ProbeOnFailStreak: 1, FrameTimeout: time.Second}},
		{"stable frames below two", WaitPolicy{StableFrames: 1, MaxSamples: 8, ProbeOnFailStreak: 1, FrameTimeout: time.Second}},
		{"max samples below stable frames", WaitPolicy{StableFrames: 4, MaxSamples: 3, ProbeOnFailStreak: 1, FrameTimeout: time.Second}},
		{"zero probe streak", WaitPolicy{StableFrames: 3, MaxSamples: 8, ProbeOnFailStreak: 0, FrameTimeout: time.Second}},
		{"zero frame timeout", WaitPolicy{StableFrames: 3, MaxSamples: 8, ProbeOnFailStreak: 1}},
	}
	for _, testCase := range cases {
		if err := testCase.policy.Validate(); err == nil {
			t.Fatalf("%s: policy accepted", testCase.name)
		}
	}
}

// TestWaitPolicyBudgetFitsHTTPCeiling pins the arithmetic the 90s ceiling rests
// on: the default dwell alone can never reach the sample guard inside one
// legal timeout, and the whole call stays inside the work window and the HTTP
// write window.
func TestWaitPolicyBudgetFitsHTTPCeiling(t *testing.T) {
	policy := DefaultWaitPolicy()
	maxWait := time.Duration(protocol.MaxWaitTimeoutSeconds) * time.Second
	// A fast caller cannot exceed the guard through the dwell alone.
	fastestSamples := float64(maxWait) / float64(policy.MinDwell)
	if math.Ceil(fastestSamples)+5 > float64(policy.MaxSamples) {
		t.Fatalf("dwell alone can reach the guard: %v samples", fastestSamples)
	}
	// The frozen phase C worst case must fit the reserve, and the reserve the
	// HTTP window.
	const phaseCWorstCase = 35 * time.Second
	if maxWait+phaseCWorstCase >= maxWait+waitWorkReserve {
		t.Fatalf("phase C does not fit the work reserve")
	}
	if maxWait+waitWorkReserve >= 90*time.Second {
		t.Fatalf("work window does not fit the HTTP ceiling")
	}
}

func TestWaitForPureSleepPublishesTimeoutFrame(t *testing.T) {
	h, reference := waitForTestHarness(t)
	before := h.device.captureCount()
	started := time.Now()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{Reference: reference, Timeout: 1})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("pure sleep failed: %+v", result.Error)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerTimeout {
		t.Fatalf("trigger = %+v", result.Wait)
	}
	if len(result.Wait.Matched) != 1 || result.Wait.Matched[0] != protocol.WaitTriggerTimeout {
		t.Fatalf("matched = %v", result.Wait.Matched)
	}
	if result.Wait.ElapsedMS < 1000 {
		t.Fatalf("elapsed = %dms, want at least the wait window", result.Wait.ElapsedMS)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("pure sleep took %v", elapsed)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatalf("pure sleep published no observation: %+v", result.Observation)
	}
	quality := result.Observation.Quality
	if quality == nil || quality.State != protocol.QualityUnknown || quality.Samples != 0 ||
		quality.StopReason != protocol.QualityStopNotSampled {
		t.Fatalf("pure sleep quality = %+v", quality)
	}
	// Exactly one terminal read: the pure sleep never polls.
	if calls := h.device.captureCount() - before; calls != 1 {
		t.Fatalf("capture calls = %d, want one terminal frame", calls)
	}
}

// TestWaitForQueryHitReusesCandidateFrame proves the early-exit contract: the
// wait stops as soon as the literal condition holds, and the verification phase
// publishes the very frame that satisfied it instead of reading the device again.
func TestWaitForQueryHitReusesCandidateFrame(t *testing.T) {
	h, reference := waitForTestHarness(t)
	h.device.scriptCaptures(markerCapture("加载中"), markerCapture("完成"))
	before := h.device.captureCount()
	started := time.Now()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "完成", HasQuery: true, Timeout: 10,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerQuery {
		t.Fatalf("trigger = %+v", result.Wait)
	}
	if len(result.Wait.Matched) != 1 || result.Wait.Matched[0] != protocol.WaitTriggerQuery {
		t.Fatalf("matched = %v", result.Wait.Matched)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatalf("no observation published")
	}
	if state := result.Observation.Quality.State; state != protocol.QualityUnstable {
		t.Fatalf("quality state = %s, want unstable after one changed frame", state)
	}
	if result.Observation.Quality.StopReason != protocol.QualityStopCondition {
		t.Fatalf("stop reason = %s", result.Observation.Quality.StopReason)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("condition hit waited the whole window: %v", elapsed)
	}
	// Two poll reads and no verification read of the device.
	if calls := h.device.captureCount() - before; calls != 2 {
		t.Fatalf("poll reads = %d, want 2", calls)
	}
}

// TestWaitForConditionCue distinguishes an early condition hit from an unstable
// sparse frame that exhausted the sample limit. Both must publish their quality
// and use it when deciding the visual classification.
func TestWaitForConditionCue(t *testing.T) {
	for _, tc := range []struct {
		name, query, trigger, stopReason string
		samples                          int
		visual, hint                     string
	}{
		{"second frame hits condition", "完成", protocol.WaitTriggerQuery, protocol.QualityStopCondition,
			2, "sparse_tree", protocol.HintSnap},
		{"sparse frames reach sample limit", "永不出现", protocol.WaitTriggerTimeout, protocol.QualityStopMaxSamples,
			3, "loading", protocol.HintWaitFor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, reference := waitForTestHarness(t)
			h.engine.cfg.Wait.MaxSamples = 3
			// The three frames have short labels, three controls, and no ProgressBar.
			h.device.scriptCaptures(markerCapture("开始"), markerCapture("完成"), markerCapture("更新"))
			result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
				&protocol.WaitForRequest{Reference: reference, Query: tc.query, HasQuery: true, Timeout: 10, Full: true})
			if apiErr != nil {
				t.Fatalf("wait_for rejected: %v", apiErr)
			}
			if result.Error != nil || result.Wait == nil || result.Wait.Trigger != tc.trigger {
				t.Fatalf("wait_for trigger = %+v, error = %+v, want %q", result.Wait, result.Error, tc.trigger)
			}
			requireFullTree(t, result.Observation, 2)
			requireQuality(t, result.Observation.Quality, protocol.QualityUnstable, tc.samples, tc.stopReason)
			requireObservationCue(t, result.Observation, tc.visual, "", tc.hint)
		})
	}
}

// TestWaitForStableNeedsThreeConsecutiveFrames is the reverse control of the
// stability gate: the sequence A,B,B is three samples but only two consecutive
// equal frames, so a sample-count implementation would release early. The gate
// must wait for A,B,B,B.
func TestWaitForStableNeedsThreeConsecutiveFrames(t *testing.T) {
	h, reference := waitForTestHarness(t)
	h.device.scriptCaptures(
		markerCapture("阶段一"), markerCapture("阶段二"),
		markerCapture("阶段二"), markerCapture("阶段二"), markerCapture("阶段二"))
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{Reference: reference, Timeout: 15, Stable: true})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerStable {
		t.Fatalf("trigger = %+v", result.Wait)
	}
	if len(result.Wait.Matched) != 0 {
		t.Fatalf("a temporal fact leaked into matched: %v", result.Wait.Matched)
	}
	quality := result.Observation.Quality
	if quality.State != protocol.QualityStable || quality.StopReason != protocol.QualityStopStable {
		t.Fatalf("quality = %+v", quality)
	}
	if quality.Samples != 4 {
		t.Fatalf("quality samples = %d, want 4 (A,B,B,B)", quality.Samples)
	}
	if calls := h.device.captureCount() - before; calls != 4 {
		t.Fatalf("poll reads = %d, want 4", calls)
	}
}

// emptyRootCapture scripts one frame that carries only the synthetic reading
// root, which is exactly the transition frame an absent-condition wait must not
// treat as a finished page.
func emptyRootCapture() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		bounds := device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400}
		return device.Capture{
			Display: display, Root: 0,
			Nodes: []device.RawNode{{
				Index: 0, Parent: -1, Children: []int{}, Depth: 0,
				DisplayID: display.ID, Visible: true,
				RawBounds: bounds, Bounds: bounds,
				Package: "com.example", ClassName: "android.widget.FrameLayout",
				HasEnabled: true, Enabled: true,
			}},
			Foreground: device.Foreground{State: device.ForegroundPresent,
				Package: "com.example", TaskID: intPtr(42)},
			CapturedAt: time.Now(),
		}, nil
	}
}

func TestWaitForAbsentQueryIgnoresTransitionFrame(t *testing.T) {
	h, reference := waitForTestHarness(t)
	h.device.scriptCaptures(
		emptyRootCapture(), markerCapture("加载中"), markerCapture("完成"))
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, AbsentQuery: "加载中", HasAbsentQuery: true, Timeout: 10,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerAbsentQuery {
		t.Fatalf("trigger = %+v", result.Wait)
	}
	if len(result.Wait.Matched) != 1 || result.Wait.Matched[0] != protocol.WaitTriggerAbsentQuery {
		t.Fatalf("matched = %v", result.Wait.Matched)
	}
	// The empty-root frame and the loading frame both had to be skipped.
	if calls := h.device.captureCount() - before; calls != 3 {
		t.Fatalf("poll reads = %d, want 3", calls)
	}
}

func TestWaitForMaxSamplesGuardReportsTimeout(t *testing.T) {
	h, reference := waitForTestHarness(t)
	policy := fastWaitPolicy()
	policy.MinDwell = time.Millisecond
	policy.MaxSamples = 4
	h.engine.cfg.Wait = policy
	// Alternating frames keep the consecutive counter at one, so the run ends
	// because of the guard rather than because the page settled.
	h.device.scriptCaptures(
		markerCapture("甲"), markerCapture("乙"), markerCapture("甲"),
		markerCapture("乙"), markerCapture("甲"))
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "永不出现", HasQuery: true, Timeout: 20,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if reads := h.device.captureCount() - before; reads != policy.MaxSamples {
		t.Fatalf("capture reads = %d, want %d with terminal reuse", reads, policy.MaxSamples)
	}
	if result.Wait.Trigger != protocol.WaitTriggerTimeout {
		t.Fatalf("trigger = %s, want timeout for the guard exit", result.Wait.Trigger)
	}
	if len(result.Wait.Matched) != 1 || result.Wait.Matched[0] != protocol.WaitTriggerTimeout {
		t.Fatalf("matched = %v", result.Wait.Matched)
	}
	quality := result.Observation.Quality
	if quality.State != protocol.QualityUnstable || quality.StopReason != protocol.QualityStopMaxSamples {
		t.Fatalf("quality = %+v, want unstable/max_samples", quality)
	}
	if quality.Samples != policy.MaxSamples {
		t.Fatalf("quality samples = %d, want %d", quality.Samples, policy.MaxSamples)
	}
}

func TestWaitForFrameTimeoutStaysTransient(t *testing.T) {
	h, reference := waitForTestHarness(t)
	policy := fastWaitPolicy()
	policy.FrameTimeout = 2 * time.Millisecond
	h.engine.cfg.Wait = policy
	// A single-read cap is a transient miss, never a device failure: the poll
	// loop must not spend a display probe on it. Only the verification
	// acquisition probes the target display.
	beforeProbes := h.device.probeCount()
	var reads int
	slowThenPage := func(f *fakeDevice, display device.Display) (device.Capture, error) {
		reads++
		if reads <= 4 {
			time.Sleep(10 * time.Millisecond)
			return device.Capture{}, errors.New("slow read")
		}
		return f.captureLocked(display)
	}
	h.device.scriptCaptures(captureStep(slowThenPage))
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "OK", HasQuery: true, Timeout: 1,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("a capped single read failed the call: %+v", result.Error)
	}
	if result.Wait.Trigger != protocol.WaitTriggerQuery {
		t.Fatalf("trigger = %s, want query", result.Wait.Trigger)
	}
	// Four capped reads were tolerated and none of them counted as a sample.
	quality := result.Observation.Quality
	if quality.State != protocol.QualityUnknown || quality.Samples != 1 ||
		quality.StopReason != protocol.QualityStopCondition {
		t.Fatalf("quality = %+v, want one condition sample", quality)
	}
	if probes := h.device.probeCount() - beforeProbes; probes != 1 {
		t.Fatalf("display probes = %d, want only the verification probe", probes)
	}
}

func TestWaitForProbesDisplayAfterRepeatedFailures(t *testing.T) {
	h, reference := waitForTestHarness(t)
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.mu.Unlock()
	h.device.scriptCaptures(failingCapture())
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "目标", HasQuery: true, Timeout: 20,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeDisplayUnavailable {
		t.Fatalf("error = %+v, want display_unavailable", result.Error)
	}
	if result.Wait != nil {
		t.Fatalf("a failed wait must not publish a wait fact: %+v", result.Wait)
	}
}

func TestWaitForRefusesExpiredSessionWithoutRenewal(t *testing.T) {
	// No clock: the expired lease stays visible to the request itself, which is
	// exactly the refusal this tool performs instead of observe's revoked path.
	h := newHarnessWithClock(t, false)
	start := h.startSession()
	expired := time.Now().Add(-time.Second)
	h.engine.mu.Lock()
	h.engine.session.ExpiresAt = expired
	h.engine.mu.Unlock()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{Reference: start.Observation.ID, Timeout: 5})
	if apiErr != nil {
		t.Fatalf("wait_for rejected at the boundary: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeSessionExpired {
		t.Fatalf("error = %+v, want session_expired", result.Error)
	}
	if result.Recovery == nil || result.Recovery.InputState != protocol.RecoveryInputRevoked {
		t.Fatalf("recovery = %+v", result.Recovery)
	}
	h.engine.mu.Lock()
	after := h.engine.session.ExpiresAt
	h.engine.mu.Unlock()
	if !after.Equal(expired) {
		t.Fatalf("expired lease was renewed to %v", after)
	}
}

func TestWaitForRenewsLeaseInsideAdmission(t *testing.T) {
	h := newHarnessWithClock(t, false)
	h.engine.cfg.Wait = fastWaitPolicy()
	start := h.startSession()
	short := time.Now().Add(2 * time.Second)
	h.engine.mu.Lock()
	h.engine.session.ExpiresAt = short
	h.engine.mu.Unlock()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{Reference: start.Observation.ID, Timeout: 1})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	h.engine.mu.Lock()
	after := h.engine.session.ExpiresAt
	h.engine.mu.Unlock()
	if !after.After(short.Add(90 * time.Second)) {
		t.Fatalf("lease was not renewed into the ordinary window: %v", after)
	}
}

func TestWaitForClientDisconnectReleasesWorkPosition(t *testing.T) {
	h := newHarnessWithClock(t, true)
	h.engine.cfg.Wait = fastWaitPolicy()
	start := h.startSession()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *protocol.Result, 1)
	go func() {
		result, _ := h.engine.WaitFor(ctx, "agent", "",
			&protocol.WaitForRequest{Reference: start.Observation.ID, Timeout: 30})
		done <- result
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !h.engine.IsBusy() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !h.engine.IsBusy() {
		t.Fatal("wait never took the work position")
	}
	cancel()
	var result *protocol.Result
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled wait did not return")
	}
	if result == nil || result.Error == nil || result.Error.Code != protocol.CodeStopped {
		t.Fatalf("result = %+v, want control_revoked", result)
	}
	if result.Wait != nil || result.Observation != nil {
		t.Fatalf("revoked wait published facts: %+v", result)
	}
	// The position is free again, so an ordinary read succeeds immediately.
	if h.engine.IsBusy() {
		t.Fatal("work position was not released")
	}
	observe, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: start.Observation.ID})
	if apiErr != nil {
		t.Fatalf("observe after cancel rejected: %v", apiErr)
	}
	if observe.Error != nil {
		t.Fatalf("observe after cancel failed: %+v", observe.Error)
	}
}

func TestWaitForRefusesBusyDevice(t *testing.T) {
	h, reference := waitForTestHarness(t)
	h.engine.mu.Lock()
	h.engine.work = &workState{
		OperationID: "Pother", Agent: "agent", Kind: WorkObserve, Phase: "capture",
		Deadline: time.Now().Add(10 * time.Second), Ctx: context.Background(),
		Cancel: func() {},
	}
	h.engine.mu.Unlock()
	defer func() {
		h.engine.mu.Lock()
		h.engine.work = nil
		h.engine.broadcastAppLocked()
		h.engine.mu.Unlock()
	}()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{Reference: reference, Timeout: 5})
	if apiErr != nil {
		t.Fatalf("wait_for rejected at the boundary: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeDeviceBusy {
		t.Fatalf("error = %+v, want device_busy", result.Error)
	}
}

func TestWaitForRejectsFoldedContradiction(t *testing.T) {
	h, reference := waitForTestHarness(t)
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "Loading", HasQuery: true,
			AbsentQuery: "loading", HasAbsentQuery: true, Timeout: 5,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeInvalidRequest {
		t.Fatalf("error = %+v, want invalid_request", result.Error)
	}
	if result.Outcome != "" {
		t.Fatalf("a read-only admission rejection invented an outcome: %q", result.Outcome)
	}
}

// TestWaitForPReferenceForcesFullTree proves that a wait anchored on an
// operation rather than an observation reads a new complete tree: there is no
// baseline to express a delta against.
func TestWaitForPReferenceForcesFullTree(t *testing.T) {
	h, reference := waitForTestHarness(t)
	if observed, apiErr := h.engine.Observe(context.Background(), "agent", "Pobserve",
		&protocol.ObserveRequest{Reference: reference}); apiErr != nil || observed.Error != nil {
		t.Fatalf("observe: %v %+v", apiErr, observed)
	}
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "Pwait",
		&protocol.WaitForRequest{Reference: "Pobserve", Timeout: 1})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	tree := result.Observation.Tree
	if tree == nil || tree.Full == nil || tree.Delta != nil {
		t.Fatalf("a P reference published a non-full tree: %+v", tree)
	}
}

func TestWaitForStatusReplayKeepsWaitFact(t *testing.T) {
	h, reference := waitForTestHarness(t)
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "Pwait",
		&protocol.WaitForRequest{Reference: reference, Timeout: 1})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Wait == nil {
		t.Fatal("no wait fact")
	}
	status, apiErr := h.engine.Status("agent", "Pwait")
	if apiErr != nil {
		t.Fatalf("status rejected: %v", apiErr)
	}
	if status.Wait == nil || status.Wait.Trigger != result.Wait.Trigger ||
		status.Wait.ElapsedMS != result.Wait.ElapsedMS {
		t.Fatalf("status wait = %+v, want %+v", status.Wait, result.Wait)
	}
	if status.Observation != nil {
		t.Fatal("a replay must not republish an observation")
	}
}

func TestWaitForImageReuseKeepsCandidateSpan(t *testing.T) {
	encoder := &fakeImageEncoder{
		data: []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01},
	}
	h := imageHarness(t, encoder)
	h.engine.cfg.Wait = fastWaitPolicy()
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.scriptCaptures(markerCapture("完成"))
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: start.Observation.ID, Query: "完成", HasQuery: true,
			Timeout: 10, Image: true,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if result.Observation.Status != "complete" {
		t.Fatalf("status = %s, want complete", result.Observation.Status)
	}
	if result.Observation.Image == nil || result.Observation.Image.Error != nil {
		t.Fatalf("image = %+v", result.Observation.Image)
	}
	// The condition frame is reused: one poll read and no second tree read.
	if calls := h.device.captureCount() - before; calls != 1 {
		t.Fatalf("reads = %d, want a single poll read", calls)
	}
	if calls := fakeImageCalls(h.device); calls != 1 {
		t.Fatalf("image reads = %d, want 1", calls)
	}
}

// ------------------------------------------------------- terminal reuse helpers

// agedWaitCapture applies an explicit capture timestamp offset to a scripted
// step, so a test can control the publish age of a frame without sleeping.
func agedWaitCapture(age time.Duration, step captureStep) captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := step(f, display)
		if err != nil {
			return capture, err
		}
		capture.CapturedAt = time.Now().Add(-age)
		return capture, nil
	}
}

// phaseAlternatingCapture labels every poll read with an alternating marker and
// labels a verification read distinctly. The work phase is read under the state
// lock from inside the device call, so the test proves which acquisition path
// produced the published frame without a wall-clock assertion.
func phaseAlternatingCapture(h *harness, terminal string) captureStep {
	var polls int
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		h.engine.mu.Lock()
		phase := ""
		if h.engine.work != nil {
			phase = h.engine.work.Phase
		}
		h.engine.mu.Unlock()
		if phase == "wait_for.tree" {
			return markerCapture(terminal)(f, display)
		}
		polls++
		marker := "甲"
		if polls%2 == 0 {
			marker = "乙"
		}
		return markerCapture(marker)(f, display)
	}
}

// slowWaitImageEncoder delays the single output encode, which is the last step
// before the commit gate, to cross the publish age deterministically.
type slowWaitImageEncoder struct {
	*fakeImageEncoder
	delay time.Duration
}

func (s slowWaitImageEncoder) Encode(source device.ImageSource, display device.Display,
	maxLongSide, quality, maxBytes int) (*protocol.ImageMeta, []byte, error) {
	time.Sleep(s.delay)
	return s.fakeImageEncoder.Encode(source, display, maxLongSide, quality, maxBytes)
}

// gateWaitImageEncoder blocks inside the output encode until the test releases
// it, which keeps a commit-gate test deterministic without a timing race.
type gateWaitImageEncoder struct {
	entered chan struct{}
	release chan struct{}
	data    []byte
	once    sync.Once
}

func (g *gateWaitImageEncoder) Encode(source device.ImageSource, display device.Display,
	maxLongSide, quality, maxBytes int) (*protocol.ImageMeta, []byte, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return &protocol.ImageMeta{
		CapturedAt: source.CapturedAt.UTC().Format(time.RFC3339),
		Width:      461, Height: 1024, MediaType: imaging.MediaTypeJPEG,
	}, g.data, nil
}

// currentTreeHasLabel reports the label of the engine's committed tree,
// independent of the result's full/delta projection.
func currentTreeHasLabel(h *harness, label string) bool {
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.current == nil || h.engine.current.Projection == nil {
		return false
	}
	return treeHasLabel(h.engine.current.Projection.Tree.Root, label)
}

// ------------------------------------------------------- terminal reuse tests

// TestWaitForTimeoutReusesLatestFreshFrame proves the timeout path publishes the
// last successful poll frame without a second hierarchy read. The published
// marker is a polling marker, never the terminal-read marker, so the assertion
// does not depend on capture counts alone.
func TestWaitForTimeoutReusesLatestFreshFrame(t *testing.T) {
	h, reference := waitForTestHarness(t)
	policy := fastWaitPolicy()
	policy.MinDwell = 100 * time.Millisecond
	policy.MaxSamples = 1000
	h.engine.cfg.Wait = policy
	h.device.scriptCaptures(phaseAlternatingCapture(h, "终帧"))
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "永不出现", HasQuery: true, Timeout: 1,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerTimeout {
		t.Fatalf("trigger = %+v, want timeout", result.Wait)
	}
	if len(result.Wait.Matched) != 1 || result.Wait.Matched[0] != protocol.WaitTriggerTimeout {
		t.Fatalf("matched = %v, want only timeout", result.Wait.Matched)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatalf("timeout published no observation: %+v", result.Observation)
	}
	quality := result.Observation.Quality
	if quality == nil || quality.State != protocol.QualityUnstable ||
		quality.StopReason != protocol.QualityStopTimeout || quality.Samples < 2 {
		t.Fatalf("quality = %+v, want unstable/timeout with at least two samples", quality)
	}
	if reads := h.device.captureCount() - before; reads != quality.Samples {
		t.Fatalf("reads = %d, samples = %d; reuse must not add a read or a sample",
			reads, quality.Samples)
	}
	tree := currentTree(t, h.engine)
	if treeHasLabel(tree.Root, "终帧") {
		t.Fatal("published a terminal re-read instead of reusing the last poll frame")
	}
	if !treeHasLabel(tree.Root, "甲") && !treeHasLabel(tree.Root, "乙") {
		t.Fatal("published tree is not a polling frame")
	}
	if status := observationStatus(h, reference); status != ObservationRetired {
		t.Fatalf("prior ready O status = %q, want retired", status)
	}
}

// TestWaitForMaxSamplesReusesLatestFreshFrame proves the sample-guard exit also
// reuses the last frame, keeps the public trigger at timeout, and reports the
// frozen max_samples stop reason.
func TestWaitForMaxSamplesReusesLatestFreshFrame(t *testing.T) {
	h, reference := waitForTestHarness(t)
	policy := fastWaitPolicy()
	policy.MinDwell = time.Millisecond
	policy.MaxSamples = 3
	h.engine.cfg.Wait = policy
	h.device.scriptCaptures(phaseAlternatingCapture(h, "终帧"))
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "永不出现", HasQuery: true, Timeout: 20,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerTimeout {
		t.Fatalf("trigger = %+v, want the public timeout projection", result.Wait)
	}
	if len(result.Wait.Matched) != 1 || result.Wait.Matched[0] != protocol.WaitTriggerTimeout {
		t.Fatalf("matched = %v, want only timeout", result.Wait.Matched)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityUnstable, 3,
		protocol.QualityStopMaxSamples)
	if reads := h.device.captureCount() - before; reads != 3 {
		t.Fatalf("reads = %d, want the three poll frames and no terminal capture", reads)
	}
	if currentTreeHasLabel(h, "终帧") {
		t.Fatal("published a terminal re-read instead of reusing the last poll frame")
	}
}

// TestWaitForLatestSuccessFollowedByFailureCapturesTerminalFrame proves a failed
// last attempt invalidates the earlier successful frame: the verification
// acquisition reads exactly one fresh terminal frame and publishes it.
//
// The first frame is aged past the dwell window, so the loop attempts the next
// read immediately, that read fails, and the remaining window is spent on the
// failure pause. The next stop therefore reaches the selector with the
// pre-failure success still in hand, which is exactly the frame the invalidation
// must drop.
func TestWaitForLatestSuccessFollowedByFailureCapturesTerminalFrame(t *testing.T) {
	h, reference := waitForTestHarness(t)
	h.engine.cfg.CaptureMaxAge = 10 * time.Second
	policy := fastWaitPolicy()
	policy.MinDwell = 2 * time.Second
	h.engine.cfg.Wait = policy
	var calls int
	h.device.scriptCaptures(func(f *fakeDevice, display device.Display) (device.Capture, error) {
		h.engine.mu.Lock()
		phase := ""
		if h.engine.work != nil {
			phase = h.engine.work.Phase
		}
		h.engine.mu.Unlock()
		if phase == "wait_for.tree" {
			return markerCapture("新")(f, display)
		}
		calls++
		if calls == 1 {
			return agedWaitCapture(5*time.Second, markerCapture("旧"))(f, display)
		}
		return device.Capture{}, errors.New("read failed after the success")
	})
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "永不出现", HasQuery: true, Timeout: 1,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatalf("no observation published: %+v", result.Observation)
	}
	if reads := h.device.captureCount() - before; reads != 3 {
		t.Fatalf("reads = %d, want one success, one failed attempt and one terminal capture", reads)
	}
	if !currentTreeHasLabel(h, "新") {
		t.Fatalf("published the pre-failure frame instead of the fresh terminal frame")
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityUnstable, 2,
		protocol.QualityStopTimeout)
}

// TestWaitForFailureThenSuccessReusesLastSuccess proves the reuse candidate is
// the latest success, not the first one before a failed attempt, and that no
// terminal read follows it.
func TestWaitForFailureThenSuccessReusesLastSuccess(t *testing.T) {
	h, reference := waitForTestHarness(t)
	policy := fastWaitPolicy()
	policy.MinDwell = time.Millisecond
	policy.MaxSamples = 2
	h.engine.cfg.Wait = policy
	var calls int
	h.device.scriptCaptures(func(f *fakeDevice, display device.Display) (device.Capture, error) {
		calls++
		switch calls {
		case 1:
			return device.Capture{}, errors.New("transient read failure")
		case 2:
			return markerCapture("先")(f, display)
		default:
			return markerCapture("后")(f, display)
		}
	})
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "永不出现", HasQuery: true, Timeout: 20,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityUnstable, 2,
		protocol.QualityStopMaxSamples)
	if reads := h.device.captureCount() - before; reads != 3 {
		t.Fatalf("reads = %d, want one failure, two successes and no terminal capture", reads)
	}
	if !currentTreeHasLabel(h, "后") {
		t.Fatalf("published an earlier success instead of the latest one")
	}
}

// TestWaitForNeverSuccessfulPollsReadOneTerminalFrame proves a wait that never
// produced a readable frame still performs exactly one terminal read (which here
// also fails) and never invents a reusable frame.
func TestWaitForNeverSuccessfulPollsReadOneTerminalFrame(t *testing.T) {
	h, reference := waitForTestHarness(t)
	policy := fastWaitPolicy()
	policy.MinDwell = 2 * time.Second
	h.engine.cfg.Wait = policy
	h.device.scriptCaptures(failingCapture())
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "永不出现", HasQuery: true, Timeout: 1,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("the terminal read failure must be a result, not a top-level rejection: %+v", result.Error)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerTimeout {
		t.Fatalf("wait = %+v, want the frozen timeout fact", result.Wait)
	}
	if result.Observation == nil || result.Observation.ID != "" ||
		result.Observation.Status != "failed" {
		t.Fatalf("observation = %+v, want a failed frame without an id", result.Observation)
	}
	if reads := h.device.captureCount() - before; reads != 2 {
		t.Fatalf("reads = %d, want one failed poll and exactly one terminal read", reads)
	}
}

// TestWaitForStaleAtSelectionCapturesTerminalFrame proves a frame that is fresh
// when read but too old at selection time is not reused: the wait falls back to
// exactly one terminal capture of a fresh frame.
func TestWaitForStaleAtSelectionCapturesTerminalFrame(t *testing.T) {
	h, reference := waitForTestHarness(t)
	h.engine.cfg.CaptureMaxAge = 100 * time.Millisecond
	policy := fastWaitPolicy()
	policy.MinDwell = 2 * time.Second
	h.engine.cfg.Wait = policy
	var calls int
	h.device.scriptCaptures(func(f *fakeDevice, display device.Display) (device.Capture, error) {
		calls++
		if calls == 1 {
			return agedWaitCapture(50*time.Millisecond, markerCapture("旧"))(f, display)
		}
		return markerCapture("新")(f, display)
	})
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "永不出现", HasQuery: true, Timeout: 1,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatalf("fresh terminal capture published no observation: %+v", result.Observation)
	}
	if reads := h.device.captureCount() - before; reads != 2 {
		t.Fatalf("reads = %d, want one poll frame and one terminal capture", reads)
	}
	if !currentTreeHasLabel(h, "新") {
		t.Fatalf("published the stale poll frame instead of the fresh terminal frame")
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityUnstable, 2,
		protocol.QualityStopTimeout)
}

// TestWaitForTerminalReuseStaleAtCommitPublishesNothing proves the selection and
// commit freshness gates are separate: a reuse candidate that ages out during
// the image/projection work returns the existing stale error, publishes no new
// O, and never falls back to a second terminal capture.
func TestWaitForTerminalReuseStaleAtCommitPublishesNothing(t *testing.T) {
	encoder := &slowWaitImageEncoder{
		fakeImageEncoder: &fakeImageEncoder{
			data: []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46},
		},
		delay: 300 * time.Millisecond,
	}
	h := imageHarness(t, encoder)
	policy := fastWaitPolicy()
	policy.MinDwell = time.Millisecond
	policy.MaxSamples = 1
	h.engine.cfg.Wait = policy
	start := h.startSession()
	h.engine.cfg.CaptureMaxAge = 200 * time.Millisecond
	setFakeImageSource(h, time.Now())
	h.device.scriptCaptures(markerCapture("复用"))
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: start.Observation.ID, Query: "永不出现", HasQuery: true,
			Timeout: 5, Image: true,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("the commit rejection must be a result, not a top-level rejection: %+v", result.Error)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerTimeout {
		t.Fatalf("wait = %+v, want the frozen timeout fact", result.Wait)
	}
	if result.Observation == nil || result.Observation.ID != "" {
		t.Fatalf("a stale reuse candidate published an observation: %+v", result.Observation)
	}
	if result.Observation.Status != "failed" || result.Observation.Error == nil ||
		result.Observation.Error.Code != protocol.CodeStaleScreen {
		t.Fatalf("observation = %+v, want the existing stale_screen failure", result.Observation)
	}
	if reads := h.device.captureCount() - before; reads != 1 {
		t.Fatalf("reads = %d, want only the poll frame and no second terminal capture", reads)
	}
	h.engine.mu.Lock()
	current := h.engine.current
	h.engine.mu.Unlock()
	if current == nil || current.ID != start.Observation.ID {
		t.Fatal("a rejected reuse candidate replaced the prior current observation")
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("prior O status = %q, want ready", status)
	}
}

// TestWaitForTerminalReuseKeepsImagePartialIndependent proves tree success and
// image staleness stay separate facts on a reused frame: the tree is published,
// the stale image marks the observation partial, and no extra tree read occurs.
func TestWaitForTerminalReuseKeepsImagePartialIndependent(t *testing.T) {
	encoder := &fakeImageEncoder{
		data: []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46},
	}
	h := imageHarness(t, encoder)
	policy := fastWaitPolicy()
	policy.MinDwell = time.Millisecond
	policy.MaxSamples = 2
	h.engine.cfg.Wait = policy
	h.engine.cfg.ImageMaxSpan = time.Millisecond
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.scriptCaptures(
		agedWaitCapture(100*time.Millisecond, markerCapture("甲")),
		agedWaitCapture(100*time.Millisecond, markerCapture("乙")))
	before := h.device.captureCount()
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: start.Observation.ID, Query: "永不出现", HasQuery: true,
			Timeout: 20, Image: true,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("an image failure must not fail the tree acquisition: %+v", result.Error)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatalf("reuse published no tree: %+v", result.Observation)
	}
	if result.Observation.Status != "partial" {
		t.Fatalf("status = %q, want partial with a stale image", result.Observation.Status)
	}
	if result.Observation.Image == nil || result.Observation.Image.Error == nil ||
		result.Observation.Image.Error.Code != protocol.CodeImageStale {
		t.Fatalf("image = %+v, want the stale image error", result.Observation.Image)
	}
	if len(result.Observation.Missing) != 1 || result.Observation.Missing[0] != "image" {
		t.Fatalf("missing = %v, want only image", result.Observation.Missing)
	}
	if !currentTreeHasLabel(h, "乙") {
		t.Fatal("published tree is not the last reused polling frame")
	}
	if reads := h.device.captureCount() - before; reads != 2 {
		t.Fatalf("reads = %d, want the two poll frames and no extra tree read", reads)
	}
	if calls := fakeImageCalls(h.device); calls != 1 {
		t.Fatalf("image reads = %d, want 1", calls)
	}
}

// TestWaitForTerminalReuseCommitGateRefusesDisplayChange proves the reuse path
// still passes through the single commit gate: a display-generation change after
// selection refuses publication, publishes no new O and keeps the prior O.
func TestWaitForTerminalReuseCommitGateRefusesDisplayChange(t *testing.T) {
	encoder := &gateWaitImageEncoder{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		data:    []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46},
	}
	h := imageHarness(t, encoder)
	policy := fastWaitPolicy()
	policy.MinDwell = time.Millisecond
	policy.MaxSamples = 1
	h.engine.cfg.Wait = policy
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.scriptCaptures(markerCapture("复用"))
	before := h.device.captureCount()
	done := make(chan *protocol.Result, 1)
	go func() {
		result, _ := h.engine.WaitFor(context.Background(), "agent", "",
			&protocol.WaitForRequest{
				Reference: start.Observation.ID, Query: "永不出现", HasQuery: true,
				Timeout: 5, Image: true,
			})
		done <- result
	}()
	select {
	case <-encoder.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("verification acquisition never reached the image encode")
	}
	// The selection is already made; only the commit gate remains.
	h.engine.mu.Lock()
	h.engine.session.Display.Generation = "44444444-4444-4444-8444-444444444444"
	h.engine.mu.Unlock()
	close(encoder.release)
	var result *protocol.Result
	select {
	case result = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return after the gate was released")
	}
	if result.Error != nil {
		t.Fatalf("the commit rejection must be a result, not a top-level rejection: %+v", result.Error)
	}
	if result.Observation == nil || result.Observation.ID != "" {
		t.Fatalf("a display-changed reuse candidate published an observation: %+v", result.Observation)
	}
	if result.Observation.Error == nil || result.Observation.Error.Code != protocol.CodeStaleScreen {
		t.Fatalf("observation = %+v, want stale_screen", result.Observation)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerTimeout {
		t.Fatalf("wait = %+v, want the frozen timeout fact", result.Wait)
	}
	if reads := h.device.captureCount() - before; reads != 1 {
		t.Fatalf("reads = %d, want only the poll frame and no terminal re-read", reads)
	}
	h.engine.mu.Lock()
	current := h.engine.current
	h.engine.mu.Unlock()
	if current == nil || current.ID != start.Observation.ID {
		t.Fatal("a rejected reuse candidate replaced the prior current observation")
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("prior O status = %q, want ready", status)
	}
}

// TestWaitForTerminalReuseCommitGateRefusesRevocation proves the same reuse path
// refuses a late publication when the request is revoked after selection: no
// new O, the prior O stays ready, and no terminal re-read is attempted.
func TestWaitForTerminalReuseCommitGateRefusesRevocation(t *testing.T) {
	encoder := &gateWaitImageEncoder{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		data:    []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46},
	}
	h := imageHarness(t, encoder)
	policy := fastWaitPolicy()
	policy.MinDwell = time.Millisecond
	policy.MaxSamples = 1
	h.engine.cfg.Wait = policy
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.scriptCaptures(markerCapture("复用"))
	before := h.device.captureCount()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *protocol.Result, 1)
	go func() {
		result, _ := h.engine.WaitFor(ctx, "agent", "",
			&protocol.WaitForRequest{
				Reference: start.Observation.ID, Query: "永不出现", HasQuery: true,
				Timeout: 5, Image: true,
			})
		done <- result
	}()
	select {
	case <-encoder.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("verification acquisition never reached the image encode")
	}
	// The selection is already made; revoke before the commit gate runs.
	cancel()
	close(encoder.release)
	var result *protocol.Result
	select {
	case result = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return after the gate was released")
	}
	if result.Error != nil {
		t.Fatalf("the commit rejection must be a result, not a top-level rejection: %+v", result.Error)
	}
	if result.Observation == nil || result.Observation.ID != "" {
		t.Fatalf("a revoked reuse candidate published an observation: %+v", result.Observation)
	}
	if result.Observation.Error == nil || result.Observation.Error.Code != protocol.CodeStopped {
		t.Fatalf("observation = %+v, want control_revoked", result.Observation)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerTimeout {
		t.Fatalf("wait = %+v, want the frozen timeout fact", result.Wait)
	}
	if reads := h.device.captureCount() - before; reads != 1 {
		t.Fatalf("reads = %d, want only the poll frame and no terminal re-read", reads)
	}
	h.engine.mu.Lock()
	current := h.engine.current
	h.engine.mu.Unlock()
	if current == nil || current.ID != start.Observation.ID {
		t.Fatal("a revoked reuse candidate replaced the prior current observation")
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("prior O status = %q, want ready", status)
	}
}
