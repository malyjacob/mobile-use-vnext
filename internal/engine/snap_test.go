package engine

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/imaging"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// snapHarness builds an engine with a recording image encoder and an optional
// clock. Tests that must observe an expired lease without the housekeeping
// ticker revoking it use startClock=false.
func snapHarness(t *testing.T, startClock bool, encoder ImageEncoder) *harness {
	t.Helper()
	dev := newFakeDevice()
	cfg := DefaultConfig("instance-test", "agent")
	cfg.CleanupTimeout = 700 * time.Millisecond
	cfg.LedgerTimeout = 200 * time.Millisecond
	projector := &testProjector{builder: observation.NewBuilder()}
	store := &fakeStore{}
	var opts []Option
	if encoder != nil {
		opts = append(opts, WithImageEncoder(encoder))
	}
	eng := New(cfg, nil, dev, store, projector, testDiffer{}, opts...)
	eng.SetDeviceReady(true)
	if startClock {
		eng.StartClock()
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		eng.Shutdown(ctx)
	})
	h := &harness{
		t: t, engine: eng, device: dev, store: store,
		appInstance: "11111111-1111-4111-8111-111111111111",
		modeGen:     "22222222-2222-4222-8222-222222222222",
	}
	h.sync(nil, nil)
	return h
}

func snapCall(h *harness, ctx context.Context, p, reference string) (*protocol.Result, *protocol.APIError) {
	if ctx == nil {
		ctx = context.Background()
	}
	return h.engine.Snap(ctx, "agent", p, &protocol.SnapRequest{Reference: reference})
}

// snapOperation reads the bounded operation facts of one snap P.
func snapOperation(h *harness, p string) (phase, code, outcome string, finished bool, recovery *protocol.Recovery) {
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	op := h.engine.operations[p]
	if op == nil {
		return "", "", "", false, nil
	}
	phase, outcome, recovery = op.Phase, op.Outcome, op.Recovery
	if op.Error != nil {
		code = op.Error.Code
	}
	return phase, code, outcome, op.State == OperationFinished, recovery
}

func setProbeSteps(h *harness, steps ...uint64) {
	h.device.mu.Lock()
	h.device.probeSteps = steps
	h.device.mu.Unlock()
}

func setImageHook(h *harness, hook func()) {
	h.device.mu.Lock()
	h.device.imageHook = hook
	h.device.mu.Unlock()
}

func TestSnapDeliversOneImageWithoutObservationChanges(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	observationID := start.Observation.ID
	capturesBefore := h.device.captureCount()
	imageAt := time.Now().Add(-time.Second)
	setFakeImageSource(h, imageAt)

	h.engine.mu.Lock()
	revisionBefore := h.engine.session.Revision
	currentBefore := h.engine.current
	expiresBefore := h.engine.session.ExpiresAt
	baselineBefore := baselineWithID(h.engine.baselines, h.engine.activeBaselineID)
	displayBefore := h.engine.session.Display
	h.engine.mu.Unlock()

	result, apiErr := snapCall(h, nil, "", observationID)
	if apiErr != nil {
		t.Fatalf("snap rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("snap failed: %+v", result.Error)
	}
	if result.OperationID == "" || result.Image == nil {
		t.Fatalf("snap success shape = %+v, want operation_id and image", result)
	}
	image := result.Image
	if image.Error != nil || !image.ReadOnly || image.MediaType != imaging.MediaTypeJPEG {
		t.Fatalf("image metadata = %+v", image)
	}
	if image.Width != 461 || image.Height != 1024 || image.CapturedAt == "" {
		t.Fatalf("image metadata is incomplete: %+v", image)
	}
	if image.CapturedAt != imageAt.UTC().Format(time.RFC3339) {
		t.Fatalf("image captured_at = %q, want the source read start", image.CapturedAt)
	}
	if image.Screen == nil || image.Screen.Width != displayBefore.Width ||
		image.Screen.Height != displayBefore.Height || image.Screen.Rotation != displayBefore.Rotation {
		t.Fatalf("image screen = %+v, want the admission display snapshot", image.Screen)
	}
	if image.Base64 != base64.StdEncoding.EncodeToString(encoder.data) {
		t.Fatal("image carrier does not carry the encoded bytes")
	}
	if encoder.callCount() != 1 {
		t.Fatalf("encoder called %d times, want exactly 1", encoder.callCount())
	}
	if fakeImageCalls(h.device) != 1 {
		t.Fatalf("CaptureImage called %d times, want exactly 1", fakeImageCalls(h.device))
	}
	if fakeImageSFID(h.device) != 7 {
		t.Fatalf("CaptureImage sfid = %d, want the admission probe value", fakeImageSFID(h.device))
	}
	if h.device.captureCount() != capturesBefore {
		t.Fatalf("snap read %d hierarchies; it must not read a tree",
			h.device.captureCount()-capturesBefore)
	}

	h.engine.mu.Lock()
	if h.engine.session.Revision != revisionBefore {
		t.Errorf("revision changed: %d -> %d", revisionBefore, h.engine.session.Revision)
	}
	if h.engine.current != currentBefore {
		t.Error("current observation changed")
	}
	if !h.engine.session.ExpiresAt.Equal(expiresBefore) {
		t.Errorf("session lease changed: %v -> %v", expiresBefore, h.engine.session.ExpiresAt)
	}
	if baselineWithID(h.engine.baselines, h.engine.activeBaselineID) != baselineBefore {
		t.Error("baseline changed")
	}
	if observation := h.engine.observations[observationID]; observation == nil ||
		observation.Status != ObservationReady {
		t.Errorf("original observation is no longer ready: %+v", observation)
	}
	op := h.engine.operations[result.OperationID]
	if op == nil || op.State != OperationFinished || op.Phase != "finished" ||
		op.Outcome != "" || op.Error != nil || op.Recovery != nil {
		t.Errorf("snap operation facts = %+v", op)
	}
	if op.SessionOrigin != h.engine.session.Origin {
		t.Errorf("snap P session origin = %q, want %q", op.SessionOrigin, h.engine.session.Origin)
	}
	h.engine.mu.Unlock()

	// A duplicate P replays the bounded summary and delivers no image.
	duplicate, apiErr := snapCall(h, nil, result.OperationID, observationID)
	if apiErr != nil {
		t.Fatalf("duplicate snap rejected: %v", apiErr)
	}
	if duplicate.Image != nil || duplicate.Error != nil {
		t.Fatalf("duplicate summary = %+v", duplicate)
	}
	if duplicate.OperationID != result.OperationID || duplicate.OperationState != OperationFinished {
		t.Fatalf("duplicate summary identity = %+v", duplicate)
	}
	if fakeImageCalls(h.device) != 1 || encoder.callCount() != 1 {
		t.Fatal("duplicate P re-read the device or re-encoded the image")
	}

	// status(O) still reports an effective input window; snap never touched it.
	status, apiErr := h.engine.Status("agent", observationID)
	if apiErr != nil {
		t.Fatalf("status(O) rejected: %v", apiErr)
	}
	if status.ObservationState != ObservationReady || status.InputState != protocol.RecoveryInputEffective {
		t.Fatalf("original O status = %+v", status)
	}
	if status.Image != nil {
		t.Fatal("status(O) carried an image")
	}

	// status(P) is the same bounded summary, never the image.
	operationStatus, apiErr := h.engine.Status("agent", result.OperationID)
	if apiErr != nil {
		t.Fatalf("status(P) rejected: %v", apiErr)
	}
	if operationStatus.Image != nil || operationStatus.Phase != "finished" ||
		operationStatus.Error != nil || operationStatus.Recovery != nil {
		t.Fatalf("status(P) = %+v", operationStatus)
	}
}

func TestSnapAdmissionRejections(t *testing.T) {
	t.Run("unknown observation", func(t *testing.T) {
		h, _ := imageTestHarness(t)
		result, apiErr := snapCall(h, nil, "", "Omissing")
		if apiErr != nil {
			t.Fatalf("snap returned access error: %v", apiErr)
		}
		if result.Error == nil || result.Error.Code != protocol.CodeObservationGone {
			t.Fatalf("error = %+v", result.Error)
		}
		phase, code, outcome, finished, recovery := snapOperation(h, result.OperationID)
		if phase != "admission" || code != protocol.CodeObservationGone ||
			outcome != "" || !finished || recovery != nil {
			t.Fatalf("operation = phase=%q code=%q outcome=%q finished=%v recovery=%+v",
				phase, code, outcome, finished, recovery)
		}
		h.engine.mu.Lock()
		origin := h.engine.operations[result.OperationID].SessionOrigin
		h.engine.mu.Unlock()
		if origin != "" {
			t.Fatalf("rejected P gained session ownership %q", origin)
		}
	})

	t.Run("other agent", func(t *testing.T) {
		h, _ := imageTestHarness(t)
		start := h.startSession()
		result, apiErr := h.engine.Snap(context.Background(), "other", "",
			&protocol.SnapRequest{Reference: start.Observation.ID})
		if apiErr != nil {
			t.Fatalf("snap returned access error: %v", apiErr)
		}
		if result.Error == nil || result.Error.Code != protocol.CodeObservationGone {
			t.Fatalf("error = %+v", result.Error)
		}
	})

	t.Run("expired lease", func(t *testing.T) {
		h := snapHarness(t, false, &fakeImageEncoder{data: []byte("jpeg")})
		start := h.startSession()
		h.engine.mu.Lock()
		h.engine.session.ExpiresAt = time.Now().Add(-time.Second)
		h.engine.mu.Unlock()
		result, apiErr := snapCall(h, nil, "", start.Observation.ID)
		if apiErr != nil {
			t.Fatalf("snap returned access error: %v", apiErr)
		}
		if result.Error == nil || result.Error.Code != protocol.CodeSessionExpired {
			t.Fatalf("error = %+v, want session_expired", result.Error)
		}
		if fakeImageCalls(h.device) != 0 {
			t.Fatal("expired lease still read the image")
		}
	})

	t.Run("self reference", func(t *testing.T) {
		h, _ := imageTestHarness(t)
		h.startSession()
		result, apiErr := snapCall(h, nil, "Pself", "Pself")
		if apiErr != nil {
			t.Fatalf("snap returned access error: %v", apiErr)
		}
		if result.Error == nil || result.Error.Code != protocol.CodeOperationUnknown {
			t.Fatalf("error = %+v, want operation_unknown", result.Error)
		}
	})

	t.Run("closed P is recognized before a new operation", func(t *testing.T) {
		h, _ := imageTestHarness(t)
		h.startSession()
		if _, apiErr := h.engine.End("agent", "", "Pclosed"); apiErr != nil {
			t.Fatalf("end(unknown) rejected: %v", apiErr)
		}
		result, apiErr := snapCall(h, nil, "Pclosed", "Pclosed")
		if apiErr != nil {
			t.Fatalf("snap returned access error: %v", apiErr)
		}
		if result.RequestClosed == nil || !*result.RequestClosed || result.Image != nil {
			t.Fatalf("closed P summary = %+v", result)
		}
	})

	t.Run("device busy", func(t *testing.T) {
		h, _ := imageTestHarness(t)
		start := h.startSession()
		h.engine.mu.Lock()
		h.engine.work = &workState{
			OperationID: "Pblocking", Agent: "agent", Kind: WorkObserve,
			Ctx: context.Background(), Deadline: time.Now().Add(time.Minute),
		}
		h.engine.mu.Unlock()
		defer func() {
			h.engine.mu.Lock()
			h.engine.work = nil
			h.engine.mu.Unlock()
		}()
		result, apiErr := snapCall(h, nil, "", start.Observation.ID)
		if apiErr != nil {
			t.Fatalf("snap returned access error: %v", apiErr)
		}
		if result.Error == nil || result.Error.Code != protocol.CodeDeviceBusy {
			t.Fatalf("error = %+v, want device_busy", result.Error)
		}
		if result.BlockingOperationID != "Pblocking" {
			t.Fatalf("blocking_operation_id = %q", result.BlockingOperationID)
		}
		if fakeImageCalls(h.device) != 0 {
			t.Fatal("busy snap read the image")
		}
	})

	t.Run("device not ready", func(t *testing.T) {
		h, _ := imageTestHarness(t)
		start := h.startSession()
		h.engine.SetDeviceReady(false)
		defer h.engine.SetDeviceReady(true)
		result, apiErr := snapCall(h, nil, "", start.Observation.ID)
		if apiErr != nil {
			t.Fatalf("snap returned access error: %v", apiErr)
		}
		if result.Error == nil || result.Error.Code != protocol.CodeDeviceUnavailable {
			t.Fatalf("error = %+v, want device_unavailable", result.Error)
		}
	})
}

func TestSnapDisplayUnavailable(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.mu.Unlock()
	result, apiErr := snapCall(h, nil, "", start.Observation.ID)
	if apiErr != nil {
		t.Fatalf("snap returned access error: %v", apiErr)
	}
	if result.Image != nil || result.Error == nil ||
		result.Error.Code != protocol.CodeDisplayUnavailable || result.HTTPStatus != 503 {
		t.Fatalf("result = %+v, want 503 display_unavailable", result)
	}
	phase, code, _, _, _ := snapOperation(h, result.OperationID)
	if phase != "failed" || code != protocol.CodeDisplayUnavailable {
		t.Fatalf("operation = phase=%q code=%q", phase, code)
	}
	if fakeImageCalls(h.device) != 0 || encoder.callCount() != 0 {
		t.Fatal("unavailable display still read or encoded an image")
	}
}

func TestSnapRejectsChangedSurfaceIdentity(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	setProbeSteps(h, 7, 8)
	result, apiErr := snapCall(h, nil, "", start.Observation.ID)
	if apiErr != nil {
		t.Fatalf("snap returned access error: %v", apiErr)
	}
	if result.Image != nil || result.Error == nil ||
		result.Error.Code != protocol.CodeDisplayUnavailable || result.HTTPStatus != 503 {
		t.Fatalf("result = %+v, want 503 display_unavailable", result)
	}
	if fakeImageCalls(h.device) != 1 || encoder.callCount() != 0 {
		t.Fatal("changed surface must discard the source before encoding")
	}
}

func TestSnapRejectsStaleSource(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now().Add(-20*time.Second))
	result, apiErr := snapCall(h, nil, "", start.Observation.ID)
	if apiErr != nil {
		t.Fatalf("snap returned access error: %v", apiErr)
	}
	if result.Image != nil || result.Error == nil ||
		result.Error.Code != protocol.CodeImageStale || resultStatus(result) != 503 {
		t.Fatalf("result = %+v, want 503 image_stale", result)
	}
	if encoder.callCount() != 1 {
		t.Fatalf("encoder called %d times, want 1", encoder.callCount())
	}
}

func TestSnapMapsEncoderFailures(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		code string
	}{
		{"too large", imaging.ErrTooLarge, protocol.CodeImageTooLarge},
		{"geometry", imaging.ErrGeometry, protocol.CodeImageCapture},
		{"invalid source", imaging.ErrInvalidSource, protocol.CodeImageCapture},
		{"unclassified", context.Canceled, protocol.CodeImageEncode},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h, encoder := imageTestHarness(t)
			start := h.startSession()
			setFakeImageSource(h, time.Now())
			encoder.failWith(testCase.err)
			result, apiErr := snapCall(h, nil, "", start.Observation.ID)
			if apiErr != nil {
				t.Fatalf("snap returned access error: %v", apiErr)
			}
			if result.Image != nil || result.Error == nil || result.Error.Code != testCase.code {
				t.Fatalf("result = %+v, want %s", result, testCase.code)
			}
			phase, _, _, _, _ := snapOperation(h, result.OperationID)
			if phase != "failed" {
				t.Fatalf("phase = %q, want failed", phase)
			}
		})
	}
}

func TestSnapStopDuringReadDiscardsImage(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	setImageHook(h, func() {
		if _, apiErr := h.engine.End("agent", "", start.Observation.ID); apiErr != nil {
			t.Errorf("end rejected: %v", apiErr)
		}
	})
	result, apiErr := snapCall(h, nil, "", start.Observation.ID)
	if apiErr != nil {
		t.Fatalf("snap returned access error: %v", apiErr)
	}
	if result.Image != nil || result.Error == nil ||
		result.Error.Code != protocol.CodeStopped || resultStatus(result) != 409 {
		t.Fatalf("result = %+v, want 409 control_revoked without image", result)
	}
	phase, code, outcome, finished, recovery := snapOperation(h, result.OperationID)
	if phase != "revoked" || code != protocol.CodeStopped || outcome != "" ||
		!finished || recovery != nil {
		t.Fatalf("operation = phase=%q code=%q outcome=%q finished=%v recovery=%+v",
			phase, code, outcome, finished, recovery)
	}
	if encoder.callCount() != 1 {
		// The image may already have been encoded; it still must not be delivered.
		t.Fatalf("encoder called %d times, want 1", encoder.callCount())
	}
}

func TestSnapRequestCancelBeforeCommit(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	setImageHook(h, cancel)
	result, apiErr := snapCall(h, ctx, "", start.Observation.ID)
	if apiErr != nil {
		t.Fatalf("snap returned access error: %v", apiErr)
	}
	if result.Image != nil || result.Error != nil {
		t.Fatalf("cancelled result = %+v, want operation_id only", result)
	}
	if result.OperationID == "" {
		t.Fatal("cancelled result lost its operation id")
	}
	phase, code, outcome, finished, recovery := snapOperation(h, result.OperationID)
	if phase != "cancelled" || code != "" || outcome != "" || !finished || recovery != nil {
		t.Fatalf("operation = phase=%q code=%q outcome=%q finished=%v recovery=%+v",
			phase, code, outcome, finished, recovery)
	}
	if encoder.callCount() != 1 {
		t.Fatalf("encoder called %d times; the read may encode, but nothing may be delivered", encoder.callCount())
	}
	// The cancelled read never revoked the session.
	status, statusErr := h.engine.Status("agent", start.Observation.ID)
	if statusErr != nil {
		t.Fatalf("status(O) rejected: %v", statusErr)
	}
	if status.InputState != protocol.RecoveryInputEffective {
		t.Fatalf("session input state = %q, want effective", status.InputState)
	}
}

func TestSnapCancelledBeforeAdmission(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, apiErr := snapCall(h, ctx, "", start.Observation.ID)
	if apiErr != nil {
		t.Fatalf("snap returned access error: %v", apiErr)
	}
	if result.Image != nil || result.Error != nil || result.OperationID == "" {
		t.Fatalf("result = %+v, want operation_id only", result)
	}
	phase, _, _, finished, _ := snapOperation(h, result.OperationID)
	if phase != "cancelled" || !finished {
		t.Fatalf("phase = %q finished=%v", phase, finished)
	}
	if h.engine.IsBusy() {
		t.Fatal("cancelled admission took the work position")
	}
	if fakeImageCalls(h.device) != 0 || encoder.callCount() != 0 {
		t.Fatal("cancelled admission read an image")
	}
}

func TestSnapDisplayGenerationChangeDuringEncode(t *testing.T) {
	encoder := newBlockingImageEncoder()
	h := snapHarness(t, true, encoder)
	start := h.startSession()
	setFakeImageSource(h, time.Now())

	type snapOutcome struct {
		result *protocol.Result
		err    *protocol.APIError
	}
	done := make(chan snapOutcome, 1)
	go func() {
		result, apiErr := snapCall(h, nil, "", start.Observation.ID)
		done <- snapOutcome{result: result, err: apiErr}
	}()
	select {
	case <-encoder.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("snap never reached the encoder")
	}
	h.engine.mu.Lock()
	h.engine.session.Display.Generation = "44444444-4444-4444-8444-444444444444"
	h.engine.mu.Unlock()
	close(encoder.release)
	got := <-done
	if got.err != nil {
		t.Fatalf("snap returned access error: %v", got.err)
	}
	if got.result.Image != nil || got.result.Error == nil ||
		got.result.Error.Code != protocol.CodeStaleScreen || resultStatus(got.result) != 409 {
		t.Fatalf("result = %+v, want 409 stale_screen", got.result)
	}
}

func TestSnapWorkDeadlineFreezesBeforeLateCommit(t *testing.T) {
	encoder := newBlockingImageEncoder()
	h := snapHarness(t, true, encoder)
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.engine.cfg.SnapTimeout = 100 * time.Millisecond

	type snapOutcome struct {
		result *protocol.Result
		err    *protocol.APIError
	}
	done := make(chan snapOutcome, 1)
	go func() {
		result, apiErr := snapCall(h, nil, "", start.Observation.ID)
		done <- snapOutcome{result: result, err: apiErr}
	}()
	select {
	case <-encoder.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("snap never reached the encoder")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		phase, _, _, _, _ := snapOperation(h, firstSnapOperationID(h))
		if phase == "work_deadline" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("snap did not freeze as work_deadline (phase %q)", phase)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if h.engine.IsBusy() {
		t.Fatal("expired snap still holds the work position")
	}
	close(encoder.release)
	got := <-done
	if got.err != nil {
		t.Fatalf("snap returned access error: %v", got.err)
	}
	if got.result.Image != nil || got.result.Error == nil ||
		got.result.Error.Code != protocol.CodeImageCapture || resultStatus(got.result) != 503 {
		t.Fatalf("result = %+v, want 503 image_capture_failed", got.result)
	}
	phase, code, outcome, _, recovery := snapOperation(h, got.result.OperationID)
	if phase != "work_deadline" || code != protocol.CodeImageCapture ||
		outcome != "" || recovery != nil {
		t.Fatalf("operation = phase=%q code=%q outcome=%q recovery=%+v",
			phase, code, outcome, recovery)
	}
}

// resultStatus is the effective HTTP status of one result: an explicit
// phase-specific hint wins, otherwise the code's fixed mapping applies.
func resultStatus(result *protocol.Result) int {
	if result == nil || result.Error == nil {
		return 0
	}
	if result.HTTPStatus != 0 {
		return result.HTTPStatus
	}
	return protocol.Status(result.Error.Code)
}

// firstSnapOperationID returns the single snap P bound to an observation's
// session, so the deadline test can poll the frozen phase before the call
// returns.
func firstSnapOperationID(h *harness) string {
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	for id, op := range h.engine.operations {
		if op.Kind == WorkSnap {
			return id
		}
	}
	return ""
}

// blockingImageEncoder blocks the single encode until release, so a test can
// interleave STOP, a display report or a work deadline with the commit window.
type blockingImageEncoder struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingImageEncoder() *blockingImageEncoder {
	return &blockingImageEncoder{entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingImageEncoder) Encode(source device.ImageSource, display device.Display,
	maxLongSide, quality, maxBytes int) (*protocol.ImageMeta, []byte, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return &protocol.ImageMeta{
		CapturedAt: source.CapturedAt.UTC().Format(time.RFC3339),
		Width:      461,
		Height:     1024,
		MediaType:  imaging.MediaTypeJPEG,
	}, []byte("jpeg-bytes"), nil
}
