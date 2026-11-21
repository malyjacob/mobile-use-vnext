package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/imaging"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// fakeImageEncoder implements ImageEncoder without decoding the source; the
// engine is responsible for the single call and the publication decisions.
type fakeImageEncoder struct {
	mu          sync.Mutex
	calls       int
	err         error
	data        []byte
	maxLongSide int
	quality     int
	maxBytes    int
}

func (f *fakeImageEncoder) Encode(source device.ImageSource, display device.Display,
	maxLongSide, quality, maxBytes int) (*protocol.ImageMeta, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.maxLongSide, f.quality, f.maxBytes = maxLongSide, quality, maxBytes
	if f.err != nil {
		return nil, nil, f.err
	}
	if len(source.Data) == 0 || source.CapturedAt.IsZero() {
		return nil, nil, imaging.ErrInvalidSource
	}
	return &protocol.ImageMeta{
		CapturedAt: source.CapturedAt.UTC().Format(time.RFC3339),
		Width:      461,
		Height:     1024,
		MediaType:  imaging.MediaTypeJPEG,
	}, f.data, nil
}

func (f *fakeImageEncoder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeImageEncoder) failWith(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

// imageTestHarness builds the ordinary harness with a recording image encoder.
func imageTestHarness(t *testing.T) (*harness, *fakeImageEncoder) {
	encoder := &fakeImageEncoder{
		data: []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01},
	}
	return imageHarness(t, encoder), encoder
}

// imageHarness is the composition root's shape: one engine plus the chosen
// encoder. A nil encoder exercises the missing-capability path.
func imageHarness(t *testing.T, encoder ImageEncoder) *harness {
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
	eng.StartClock()
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

// realImageEncoder is the same adapter the composition root supplies.
type realImageEncoder struct{}

func (realImageEncoder) Encode(source device.ImageSource, display device.Display,
	maxLongSide, quality, maxBytes int) (*protocol.ImageMeta, []byte, error) {
	return imaging.Encode(source, display, imaging.Policy{
		MaxLongSide: maxLongSide, Quality: quality, MaxBytes: maxBytes,
	})
}

func setFakeImageSource(h *harness, capturedAt time.Time) {
	h.device.mu.Lock()
	h.device.imageSource = &device.ImageSource{
		Data: []byte("png-source-bytes"), CapturedAt: capturedAt,
	}
	h.device.mu.Unlock()
}

func fakeImageCalls(dev *fakeDevice) int {
	dev.mu.Lock()
	defer dev.mu.Unlock()
	return dev.imageCalls
}

func fakeImageSFID(dev *fakeDevice) uint64 {
	dev.mu.Lock()
	defer dev.mu.Unlock()
	return dev.imageSFID
}

func observeImage(h *harness, observationID string) *protocol.Observation {
	h.t.Helper()
	result, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: observationID, Image: true})
	if apiErr != nil {
		h.t.Fatalf("observe rejected: %v", apiErr)
	}
	if result.Observation == nil {
		h.t.Fatal("observe returned no observation")
	}
	return result.Observation
}

func assertImageSlotFailure(t *testing.T, obs *protocol.Observation, code string) {
	t.Helper()
	if obs.Status != "partial" {
		t.Fatalf("status = %q, want partial", obs.Status)
	}
	if len(obs.Missing) != 1 || obs.Missing[0] != "image" {
		t.Fatalf("missing = %v, want [image]", obs.Missing)
	}
	if obs.Image == nil || obs.Image.Error == nil {
		t.Fatalf("image slot does not carry an error: %+v", obs.Image)
	}
	if obs.Image.Error.Code != code {
		t.Fatalf("image error = %q, want %q", obs.Image.Error.Code, code)
	}
	if obs.Image.Base64 != "" || obs.Image.Width != 0 || obs.Image.Height != 0 ||
		obs.Image.CapturedAt != "" {
		t.Fatalf("failed image slot carries material: %+v", obs.Image)
	}
}

func TestObserveImagePublishesOneObservationWithImage(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	// The image read starts slightly before the tree read; a normal image is
	// never rejected merely for being older than the tree.
	imageAt := time.Now().Add(-2 * time.Second)
	setFakeImageSource(h, imageAt)

	h.engine.mu.Lock()
	observationsBefore := len(h.engine.observations)
	h.engine.mu.Unlock()

	obs := observeImage(h, start.Observation.ID)
	if obs.Status != "complete" {
		t.Fatalf("status = %q, want complete", obs.Status)
	}
	if obs.ID == "" || obs.ID == start.Observation.ID {
		t.Fatalf("expected one new observation, got %+v", obs)
	}
	if len(obs.Missing) != 0 {
		t.Fatalf("complete observation has missing fields: %v", obs.Missing)
	}
	image := obs.Image
	if image == nil {
		t.Fatal("complete observation has no image")
	}
	if image.Error != nil || image.ReadOnly || image.Screen != nil {
		t.Fatalf("attached image carries failure/read-only fields: %+v", image)
	}
	if image.MediaType != imaging.MediaTypeJPEG || image.Width <= 0 || image.Height <= 0 ||
		image.CapturedAt == "" {
		t.Fatalf("attached image metadata is incomplete: %+v", image)
	}
	if image.CapturedAt != imageAt.UTC().Format(time.RFC3339) {
		t.Fatalf("image captured_at = %q, want the source read start", image.CapturedAt)
	}
	actionable, parseErr := time.Parse(time.RFC3339Nano, obs.ActionableUntil)
	if parseErr != nil {
		t.Fatalf("actionable_until = %q: %v", obs.ActionableUntil, parseErr)
	}
	if want := imageAt.Add(h.engine.cfg.ObservationLifetime); !actionable.Equal(want.UTC()) {
		t.Fatalf("actionable_until = %v, want the earlier read start + lifetime %v",
			actionable, want.UTC())
	}
	wantBase64 := base64.StdEncoding.EncodeToString(encoder.data)
	if image.Base64 != wantBase64 {
		t.Fatalf("image carrier = %q, want the encoded bytes", image.Base64)
	}
	if encoder.callCount() != 1 {
		t.Fatalf("encoder called %d times, want exactly 1", encoder.callCount())
	}
	if fakeImageCalls(h.device) != 1 {
		t.Fatalf("CaptureImage called %d times, want 1", fakeImageCalls(h.device))
	}
	if encoder.maxLongSide != h.engine.cfg.ImageMaxLongSide ||
		encoder.quality != h.engine.cfg.ImageQuality ||
		encoder.maxBytes != h.engine.cfg.MaxImageBytes {
		t.Fatalf("encoder policy = (%d,%d,%d), want the engine image policy",
			encoder.maxLongSide, encoder.quality, encoder.maxBytes)
	}

	h.engine.mu.Lock()
	observationsAfter := len(h.engine.observations)
	h.engine.mu.Unlock()
	if observationsAfter != observationsBefore+1 {
		t.Fatalf("observations = %d, want %d", observationsAfter, observationsBefore+1)
	}

	// The image bytes are a transport carrier of the observation only; the
	// tree text carries no image material.
	treeJSON, err := json.Marshal(obs.Tree)
	if err != nil {
		t.Fatalf("marshal tree: %v", err)
	}
	if strings.Contains(string(treeJSON), "base64") ||
		strings.Contains(string(treeJSON), encoder.dataBase64()) {
		t.Fatalf("tree text carries image material")
	}
	observationJSON, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("marshal observation: %v", err)
	}
	if !strings.Contains(string(observationJSON), `"base64"`) {
		t.Fatalf("observation has no base64 carrier: %s", observationJSON)
	}
}

func (f *fakeImageEncoder) dataBase64() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return base64.StdEncoding.EncodeToString(f.data)
}

func TestObserveImageFailureKeepsValidObservation(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()

	// No image source is configured: the device read fails while the tree
	// acquisition is unaffected.
	obs := observeImage(h, start.Observation.ID)
	if obs.ID == "" {
		t.Fatal("image failure discarded the core observation")
	}
	assertImageSlotFailure(t, obs, protocol.CodeImageCapture)
	if encoder.callCount() != 0 {
		t.Fatalf("encoder called %d times without a source", encoder.callCount())
	}
	h.engine.mu.Lock()
	current := h.engine.current
	h.engine.mu.Unlock()
	if current == nil || current.ID != obs.ID {
		t.Fatalf("committed observation is not the current one")
	}
}

func TestImageUsesRuntimeDisplaySurfaceID(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	// A stale snapshot surface id must never drive the device read; the probe
	// value of the same acquisition is the bound one.
	h.engine.mu.Lock()
	h.engine.session.Display.SFID = 999
	h.engine.mu.Unlock()

	obs := observeImage(h, start.Observation.ID)
	if obs.Image == nil || obs.Image.Error != nil {
		t.Fatalf("image not attached: %+v", obs.Image)
	}
	h.device.mu.Lock()
	probeSFID := h.device.sfid
	h.device.mu.Unlock()
	if got := fakeImageSFID(h.device); got != probeSFID {
		t.Fatalf("CaptureImage sfid = %d, want the probe value %d", got, probeSFID)
	}
	if encoder.callCount() != 1 {
		t.Fatalf("encoder called %d times, want 1", encoder.callCount())
	}
}

func TestObserveDisplayUnavailableSkipsImage(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.mu.Unlock()

	obs := observeImage(h, start.Observation.ID)
	if obs.ID != "" {
		t.Fatalf("failed acquisition received an observation id %q", obs.ID)
	}
	if obs.Status != "failed" {
		t.Fatalf("status = %q, want failed", obs.Status)
	}
	if len(obs.Missing) != 1 || obs.Missing[0] != "tree" {
		t.Fatalf("missing = %v, want [tree]", obs.Missing)
	}
	if obs.Error == nil || obs.Error.Code != protocol.CodeDisplayUnavailable {
		t.Fatalf("core error = %+v, want display_unavailable", obs.Error)
	}
	if obs.Image == nil || obs.Image.Error == nil ||
		obs.Image.Error.Code != protocol.CodeImageCapture {
		t.Fatalf("image slot = %+v, want image_capture_failed", obs.Image)
	}
	if fakeImageCalls(h.device) != 0 {
		t.Fatal("image read ran without a target display")
	}
	if encoder.callCount() != 0 {
		t.Fatal("encoder ran without a target display")
	}
}

func TestImageEncodeErrorsMapToPublicCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"too large", imaging.ErrTooLarge, protocol.CodeImageTooLarge},
		{"geometry", imaging.ErrGeometry, protocol.CodeImageCapture},
		{"invalid source", imaging.ErrInvalidSource, protocol.CodeImageCapture},
		{"unknown", errors.New("encoder failure"), protocol.CodeImageEncode},
		{"wrapped too large", errors.Join(imaging.ErrTooLarge), protocol.CodeImageTooLarge},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h, encoder := imageTestHarness(t)
			start := h.startSession()
			setFakeImageSource(h, time.Now())
			encoder.failWith(testCase.err)

			obs := observeImage(h, start.Observation.ID)
			if obs.ID == "" {
				t.Fatal("encode failure discarded the core observation")
			}
			assertImageSlotFailure(t, obs, testCase.code)
			if encoder.callCount() != 1 {
				t.Fatalf("encoder called %d times, want 1", encoder.callCount())
			}
		})
	}
}

func TestObserveImageWithoutEncoderReportsEncodeFailure(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.engine.imaging = nil

	obs := observeImage(h, start.Observation.ID)
	if obs.ID == "" {
		t.Fatal("missing encoder discarded the core observation")
	}
	assertImageSlotFailure(t, obs, protocol.CodeImageEncode)
	if fakeImageCalls(h.device) != 0 {
		t.Fatal("CaptureImage ran without an encoder")
	}
	if encoder.callCount() != 0 {
		t.Fatal("removed encoder was still called")
	}
}

func TestObserveTreeFailureReturnsReadOnlyImage(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.mu.Lock()
	h.device.captureErrs = true
	h.device.mu.Unlock()

	obs := observeImage(h, start.Observation.ID)
	if obs == nil {
		t.Fatal("tree failure returned no observation")
	}
	if obs.ID != "" {
		t.Fatalf("failed acquisition received an observation id %q", obs.ID)
	}
	if obs.Status != "partial" {
		t.Fatalf("status = %q, want partial", obs.Status)
	}
	if len(obs.Missing) != 1 || obs.Missing[0] != "tree" {
		t.Fatalf("missing = %v, want [tree]", obs.Missing)
	}
	if obs.Error == nil || obs.Error.Code != protocol.CodeHierarchyFailed {
		t.Fatalf("core error = %+v, want hierarchy_unavailable", obs.Error)
	}
	if obs.Screen == nil || obs.Screen.Width <= 0 {
		t.Fatalf("partial observation has no target screen: %+v", obs.Screen)
	}
	image := obs.Image
	if image == nil || image.Error != nil || !image.ReadOnly {
		t.Fatalf("read-only image slot = %+v", image)
	}
	if image.Screen == nil || image.Screen.Width != obs.Screen.Width {
		t.Fatalf("image screen = %+v, want the target display", image.Screen)
	}
	if image.Base64 == "" || image.Width <= 0 || image.CapturedAt == "" {
		t.Fatalf("read-only image carries no material: %+v", image)
	}
	if encoder.callCount() != 1 {
		t.Fatalf("encoder called %d times, want 1", encoder.callCount())
	}
	h.engine.mu.Lock()
	current := h.engine.current
	h.engine.mu.Unlock()
	if current == nil || current.ID != start.Observation.ID {
		t.Fatal("failed acquisition replaced the committed observation")
	}
}

func TestStaleImageIsNotAttached(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now().Add(-5*time.Second))

	obs := observeImage(h, start.Observation.ID)
	if obs.ID == "" {
		t.Fatal("stale image discarded the core observation")
	}
	assertImageSlotFailure(t, obs, protocol.CodeImageStale)
	if encoder.callCount() != 1 {
		t.Fatalf("encoder called %d times, want 1", encoder.callCount())
	}
	if obs.ActionableUntil == "" {
		t.Fatal("committed observation has no actionable_until")
	}
}

func TestObserveWithoutImageSkipsCapture(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())

	result, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: start.Observation.ID})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	if result.Observation == nil || result.Observation.Status != "complete" {
		t.Fatalf("observation = %+v", result.Observation)
	}
	if result.Observation.Image != nil {
		t.Fatalf("image returned without being requested: %+v", result.Observation.Image)
	}
	if len(result.Observation.Missing) != 0 {
		t.Fatalf("missing = %v, want none", result.Observation.Missing)
	}
	if fakeImageCalls(h.device) != 0 {
		t.Fatalf("CaptureImage called %d times without a request", fakeImageCalls(h.device))
	}
	if encoder.callCount() != 0 {
		t.Fatalf("encoder called %d times without a request", encoder.callCount())
	}
}

func tapButtonRequest(t *testing.T, h *harness, observationID string) *protocol.ActRequest {
	t.Helper()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	return &protocol.ActRequest{
		Reference: observationID,
		Operation: protocol.Operation{
			Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref},
		},
		Image: true,
	}
}

func TestActWithImageAttachesPostImage(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())

	request := tapButtonRequest(t, h, start.Observation.ID)
	result, apiErr := h.engine.Act(context.Background(), "agent", "", request)
	if apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q, want executed", result.Outcome)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatalf("act returned no post observation: %+v", result.Observation)
	}
	image := result.Observation.Image
	if image == nil || image.Error != nil || image.ReadOnly || image.Screen != nil {
		t.Fatalf("post image = %+v", image)
	}
	if image.Base64 == "" {
		t.Fatal("post image has no carrier")
	}
	if encoder.callCount() != 1 || fakeImageCalls(h.device) != 1 {
		t.Fatalf("image work = encoder %d, device %d; want one each",
			encoder.callCount(), fakeImageCalls(h.device))
	}
}

func TestActPostTreeFailureKeepsExecutedWithReadOnlyImage(t *testing.T) {
	h, encoder := imageTestHarness(t)
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	// The action's live target revalidation does not encode a public tree, so
	// shrinking the full-tree budget makes only the post tree encoding fail.
	h.engine.cfg.MaxFullTreeBytes = 16

	request := tapButtonRequest(t, h, start.Observation.ID)
	result, apiErr := h.engine.Act(context.Background(), "agent", "", request)
	if apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q, want executed", result.Outcome)
	}
	requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, protocol.RecoveryCausePostObservationFailed)
	obs := result.Observation
	if obs == nil || obs.ID != "" {
		t.Fatalf("post failure returned %+v, want no observation id", obs)
	}
	if obs.Status != "partial" || len(obs.Missing) != 1 || obs.Missing[0] != "tree" {
		t.Fatalf("post failure shape = %+v", obs)
	}
	if obs.Error == nil || obs.Error.Code != protocol.CodeTreeTooLarge {
		t.Fatalf("post failure error = %+v", obs.Error)
	}
	if obs.Screen == nil || obs.Image == nil || !obs.Image.ReadOnly {
		t.Fatalf("read-only image = %+v, screen = %+v", obs.Image, obs.Screen)
	}
	if obs.Image.Error != nil || obs.Image.Base64 == "" {
		t.Fatalf("read-only image slot = %+v", obs.Image)
	}
	if encoder.callCount() != 1 {
		t.Fatalf("encoder called %d times, want 1", encoder.callCount())
	}
}

// testPNG returns one real PNG of the target display geometry.
func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y += 8 {
		for x := 0; x < width; x += 8 {
			img.SetRGBA(x, y, color.RGBA{R: 0x20, G: 0x80, B: 0xc0, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode source png: %v", err)
	}
	return buf.Bytes()
}

func TestImageWithRealEncoderProducesJPEGCarrier(t *testing.T) {
	h := imageHarness(t, realImageEncoder{})
	start := h.startSession()
	h.device.mu.Lock()
	h.device.imageSource = &device.ImageSource{
		Data: testPNG(t, h.device.width, h.device.height), CapturedAt: time.Now(),
	}
	h.device.mu.Unlock()

	obs := observeImage(h, start.Observation.ID)
	image := obs.Image
	if image == nil || image.Error != nil {
		t.Fatalf("real encoder image = %+v", image)
	}
	if image.MediaType != imaging.MediaTypeJPEG || image.Base64 == "" {
		t.Fatalf("real encoder metadata = %+v", image)
	}
	// The fake 1080x2400 screen must scale to the documented 461x1024 output,
	// so the engine's own image budget is proven and not only the pure encoder.
	if image.Width != 461 || image.Height != 1024 {
		t.Fatalf("output = %dx%d, want 461x1024", image.Width, image.Height)
	}
	if image.Width > h.engine.cfg.ImageMaxLongSide || image.Height > h.engine.cfg.ImageMaxLongSide {
		t.Fatalf("output %dx%d exceeds the long-side budget", image.Width, image.Height)
	}
	raw, err := base64.StdEncoding.DecodeString(image.Base64)
	if err != nil || len(raw) < 3 || raw[0] != 0xff || raw[1] != 0xd8 || raw[2] != 0xff {
		t.Fatalf("carrier is not a JPEG: %d bytes, %v", len(raw), err)
	}
	if len(raw) > h.engine.cfg.MaxImageBytes {
		t.Fatalf("carrier %d bytes exceeds the image budget", len(raw))
	}
}
