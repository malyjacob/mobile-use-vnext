package engine

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
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
