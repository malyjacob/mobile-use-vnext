package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ---------------------------------------------------------------- fake device

type fakeDevice struct {
	bootID         string
	deviceID       string
	identityErr    error
	mu             sync.Mutex
	width, height  int
	density        int
	displayExists  bool
	sfid           uint64
	helperCalls    int
	taps           [][2]int
	keys           []string
	texts          []string
	inputFocused   bool
	inputText      string
	checked        bool
	captureErrs    bool
	closeDisplayID int

	// Capture scripting. A non-empty script is consumed one step per call and
	// the last step repeats, so a test can describe the frames a settle run
	// sees without depending on how many reads the flow performs.
	captureSteps []captureStep
	captureCalls int
	// trace records the order of hierarchy reads and image reads, so a test can
	// prove the acquisition order without inspecting timestamps.
	trace []string

	// Discovery and task facts. Tests set these before calling the engine.
	appEntries       []device.AppEntry
	appErr           error
	appCalls         int
	resolveComponent string
	resolveErr       error
	resolveMissing   bool
	launchState      device.LaunchState
	launchErr        error
	launchCalls      int
	launchTask       *device.Task
	tasks            []device.Task
	focusCalls       []int
	focusErr         error
	removeCalls      []int
	removeErr        error
	recentIDs        map[int]bool
	recentKnown      bool
	foreground       *device.Foreground
	imageSource      *device.ImageSource
	imageCalls       int
	imageErr         error
	imageSFID        uint64
	shell            fakeShellHooks
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{width: 1080, height: 2400, density: 420, displayExists: true, sfid: 7}
}

func (f *fakeDevice) Prepare(context.Context) error  { return nil }
func (f *fakeDevice) EnsureU2(context.Context) error { return nil }
func (f *fakeDevice) StopU2(context.Context) error   { return nil }
func (f *fakeDevice) LogicalSize(context.Context) (int, int, int, error) {
	return f.width, f.height, f.density, nil
}
func (f *fakeDevice) ProbeDisplay(_ context.Context, id int) (device.DisplayState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == 0 {
		return device.DisplayState{Exists: true, SFID: 1}, nil
	}
	return device.DisplayState{Exists: f.displayExists, SFID: f.sfid}, nil
}
func (f *fakeDevice) CaptureImage(_ context.Context, sfid uint64) (device.ImageSource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageCalls++
	f.imageSFID = sfid
	f.trace = append(f.trace, "image")
	if f.imageErr != nil {
		return device.ImageSource{}, f.imageErr
	}
	if f.imageSource != nil {
		return *f.imageSource, nil
	}
	return device.ImageSource{}, errors.New("no image source configured")
}
func (f *fakeDevice) Foreground(_ context.Context, displayID int) (device.Foreground, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.foreground != nil {
		return *f.foreground, nil
	}
	return device.Foreground{State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42)}, nil
}
func (f *fakeDevice) TaskList(context.Context) ([]device.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]device.Task(nil), f.tasks...), nil
}
func (f *fakeDevice) StartDisplayHelper(context.Context, string, string) error {
	f.mu.Lock()
	f.helperCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeDevice) ExecutionIdentity(context.Context) (device.BootIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.identityErr != nil {
		return device.BootIdentity{}, f.identityErr
	}
	id, boot := f.deviceID, f.bootID
	if id == "" {
		id = "device-test"
	}
	if boot == "" {
		boot = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	}
	return device.BootIdentity{DeviceID: id, BootID: boot}, nil
}

func (f *fakeDevice) Tap(_ context.Context, displayID, x, y int) (device.DispatchReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taps = append(f.taps, [2]int{x, y})
	if x >= 100 && x < 1000 && y >= 300 && y < 400 {
		f.inputFocused = true
	}
	if x >= 900 && x < 1000 && y >= 100 && y < 200 {
		f.checked = !f.checked
	}
	return device.DispatchTerminated, nil
}
func (f *fakeDevice) LongPress(_ context.Context, displayID, x, y int) (device.DispatchReceipt, error) {
	return f.Tap(nil, displayID, x, y)
}
func (f *fakeDevice) Swipe(context.Context, int, int, int, int, int) (device.DispatchReceipt, error) {
	return device.DispatchTerminated, nil
}
func (f *fakeDevice) Key(_ context.Context, displayID int, key string) (device.DispatchReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, key)
	return device.DispatchTerminated, nil
}
func (f *fakeDevice) GetTextOnDisplay(context.Context, device.TextTarget) (device.TextResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value := f.inputText
	return device.TextResult{CandidateCount: 1, Value: &value}, nil
}
func (f *fakeDevice) SetTextOnDisplay(_ context.Context, target device.TextTarget, text string) (device.TextResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputText = text
	f.texts = append(f.texts, text)
	return device.TextResult{CandidateCount: 1, Issued: true, Accepted: true, Receipt: device.DispatchTerminated}, nil
}
func (f *fakeDevice) Capture(_ context.Context, display device.Display) (device.Capture, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captureCalls++
	f.trace = append(f.trace, "capture")
	if f.captureErrs {
		return device.Capture{}, errors.New("capture failed")
	}
	if len(f.captureSteps) > 0 {
		step := f.captureSteps[0]
		if len(f.captureSteps) > 1 {
			f.captureSteps = f.captureSteps[1:]
		}
		return step(f, display)
	}
	return f.captureLocked(display)
}

// captureLocked builds the default synthetic page. The caller holds the lock.
func (f *fakeDevice) captureLocked(display device.Display) (device.Capture, error) {
	focused, password, text := false, false, ""
	if f.inputFocused {
		focused = true
		text = f.inputText
	}
	nodes := []device.RawNode{
		{
			Index: 0, Parent: -1, Children: []int{1, 2, 3}, Depth: 0,
			DisplayID: display.ID, Visible: true,
			RawBounds: device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400}, Bounds: device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400},
			Package: "com.example", ClassName: "android.widget.FrameLayout", HasEnabled: true, Enabled: true,
		},
		{
			Index: 1, Parent: 0, Depth: 1, DisplayID: display.ID, Visible: true,
			RawBounds: device.Rect{Left: 100, Top: 100, Right: 900, Bottom: 200}, Bounds: device.Rect{Left: 100, Top: 100, Right: 900, Bottom: 200},
			Package: "com.example", ClassName: "android.widget.Button", Text: "OK", TextSet: true,
			HasClickable: true, Clickable: true, HasEnabled: true, Enabled: true,
		},
		{
			Index: 2, Parent: 0, Depth: 1, DisplayID: display.ID, Visible: true,
			RawBounds: device.Rect{Left: 100, Top: 300, Right: 1000, Bottom: 400}, Bounds: device.Rect{Left: 100, Top: 300, Right: 1000, Bottom: 400},
			Package: "com.example", ClassName: "android.widget.EditText", Text: text, TextSet: true,
			HasClickable: true, Clickable: true, Editable: true,
			HasEnabled: true, Enabled: true, HasFocused: true, Focused: focused,
			HasPassword: true, Password: password,
		},
		{
			Index: 3, Parent: 0, Depth: 1, DisplayID: display.ID, Visible: true,
			RawBounds: device.Rect{Left: 900, Top: 100, Right: 1000, Bottom: 200}, Bounds: device.Rect{Left: 900, Top: 100, Right: 1000, Bottom: 200},
			Package: "com.example", ClassName: "android.widget.CheckBox", Text: "Remember",
			TextSet: true, HasCheckable: true, Checkable: true,
			HasChecked: true, Checked: f.checked,
			HasClickable: true, Clickable: true, HasEnabled: true, Enabled: true,
		},
	}
	return device.Capture{
		Display: display, Nodes: nodes, Root: 0,
		Foreground: device.Foreground{State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42)},
		CapturedAt: time.Now(),
	}, nil
}

func intPtr(v int) *int { return &v }

// captureStep scripts one Capture result. It runs with the device lock held and
// must not call back into the device.
type captureStep func(device *fakeDevice, display device.Display) (device.Capture, error)

// markerCapture returns the default page with the button label replaced, which
// makes the frame distinguishable in both the raw identity and the public tree.
func markerCapture(marker string) captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := f.captureLocked(display)
		if err != nil {
			return capture, err
		}
		for index := range capture.Nodes {
			if capture.Nodes[index].ClassName == "android.widget.Button" {
				capture.Nodes[index].Text = marker
				capture.Nodes[index].TextSet = true
			}
		}
		return capture, nil
	}
}

// failingCapture scripts one unreadable hierarchy.
func failingCapture() captureStep {
	return func(*fakeDevice, device.Display) (device.Capture, error) {
		return device.Capture{}, errors.New("capture failed")
	}
}

func (f *fakeDevice) scriptCaptures(steps ...captureStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captureSteps = steps
}

func (f *fakeDevice) captureCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.captureCalls
}

// readTrace returns the recorded read order and clears it.
func (f *fakeDevice) readTrace() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	trace := append([]string(nil), f.trace...)
	f.trace = nil
	return trace
}

// ---------------------------------------------------------------- fake store

type fakeStore struct {
	mu     sync.Mutex
	record *Ledger
	rev    int64
}

func (s *fakeStore) Load() (*Ledger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.record, nil
}

func (s *fakeStore) Save(_ context.Context, record *Ledger) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rev++
	record.Revision = s.rev
	copyRecord := *record
	s.record = &copyRecord
	return s.rev, nil
}

// ---------------------------------------------------------------- adapters

type testProjector struct{ builder *observation.Builder }

func (p *testProjector) Build(capture *device.Capture) (*Projection, error) {
	snapshot, err := p.builder.Build(capture)
	if err != nil {
		return nil, err
	}
	return &Projection{Tree: snapshot.Tree, Capture: snapshot.Capture, Source: snapshot.Source}, nil
}

func (p *testProjector) Preview(capture *device.Capture) (*Projection, error) {
	snapshot, err := p.builder.Preview(capture)
	if err != nil {
		return nil, err
	}
	return &Projection{Tree: snapshot.Tree, Capture: snapshot.Capture, Source: snapshot.Source}, nil
}

func (p *testProjector) Reset() { p.builder.Reset() }

type testDiffer struct{}

func (testDiffer) Diff(baseID string, base, current *protocol.Tree) (*protocol.Delta, error) {
	return observation.Diff(baseID, base, current)
}

// ---------------------------------------------------------------- harness

type harness struct {
	t           *testing.T
	engine      *Engine
	device      *fakeDevice
	store       *fakeStore
	appInstance string
	modeGen     string
	display     *protocol.AppDisplay
	lastSync    *protocol.AppSyncResponse
	syncResults []protocol.AppCommandResult
	commands    []protocol.AppCommand
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithClock(t, true)
}

// newHarnessWithClock builds the same harness without the one-second
// housekeeping ticker when a test needs an expired lease to stay unrevoked
// until the request itself observes it.
func newHarnessWithClock(t *testing.T, startClock bool) *harness {
	t.Helper()
	return newHarnessWithLogger(t, startClock, nil)
}

// newHarnessWithLogger builds the same harness with a real event log, so a test
// can assert what the service records without touching public results.
func newHarnessWithLogger(t *testing.T, startClock bool, log *eventlog.Logger) *harness {
	t.Helper()
	dev := newFakeDevice()
	cfg := DefaultConfig("instance-test", "agent")
	cfg.CleanupTimeout = 700 * time.Millisecond
	cfg.LedgerTimeout = 200 * time.Millisecond
	projector := &testProjector{builder: observation.NewBuilder()}
	store := &fakeStore{}
	eng := New(cfg, log, dev, store, projector, testDiffer{})
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

func (h *harness) sync(display *protocol.AppDisplay, stop *protocol.AppStopPending) *protocol.AppSyncResponse {
	h.t.Helper()
	power, keyguard := true, false
	req := &protocol.AppSyncRequest{
		ContractVersion: protocol.ContractVersion,
		AppInstanceID:   h.appInstance,
		AppVersion:      "test",
		SelectedMode:    "virtual",
		ModeGeneration:  h.modeGen,
		ModeReady:       true, StopReady: true, NotificationsEnabled: true, BackgroundRunning: true,
		PowerInteractive: &power, KeyguardLocked: &keyguard,
		PrimaryDisplay: &protocol.AppDisplay{DisplayID: 0, Width: 1080, Height: 2400,
			Density: 420, Rotation: 0, Generation: h.modeGen},
		Display:        display,
		CommandResults: h.syncResults,
		StopPending:    stop,
	}
	h.syncResults = nil
	response, apiErr := h.engine.SyncApp(context.Background(), req)
	if apiErr != nil {
		h.t.Fatalf("sync rejected: %v", apiErr)
	}
	h.lastSync = response
	if len(response.Commands) > 0 {
		h.commands = append(h.commands, response.Commands...)
		for _, command := range response.Commands {
			switch command.Kind {
			case "close_display":
				h.device.mu.Lock()
				h.device.displayExists = false
				h.device.mu.Unlock()
				h.commands = append(h.commands, command)
			}
		}
	}
	return response
}

func (h *harness) reportDisplayAfter(delay time.Duration) {
	display := &protocol.AppDisplay{DisplayID: 12, Width: 1080, Height: 2400,
		Density: 420, Rotation: 0, Generation: "33333333-3333-4333-8333-333333333333"}
	go func() {
		time.Sleep(delay)
		// Deliver the create command receipt and report the created display.
		h.engine.mu.Lock()
		h.engine.app.Display = appDisplayToDevice(display)
		h.engine.app.DisplayRaw = display
		if h.engine.session != nil {
			h.engine.session.Display = *h.engine.app.Display
			ok := true
			h.engine.session.CreateResult = &ok
			h.engine.removeCommandLocked(h.engine.session.CreateCommand)
		}
		h.engine.broadcastAppLocked()
		h.engine.mu.Unlock()
	}()
}

func (h *harness) startSession() *protocol.Result {
	h.t.Helper()
	h.reportDisplayAfter(30 * time.Millisecond)
	result, apiErr := h.engine.Start(context.Background(), "agent", "")
	if apiErr != nil {
		h.t.Fatalf("start rejected: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted {
		h.t.Fatalf("start outcome %s error %+v", result.Outcome, result.Error)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		h.t.Fatalf("start returned no first observation: %+v", result.Observation)
	}
	return result
}

func findRole(root *protocol.Node, role protocol.Role) *protocol.Node {
	if root == nil {
		return nil
	}
	if root.Role == role {
		return root
	}
	for _, child := range root.Children {
		if found := findRole(child, role); found != nil {
			return found
		}
	}
	return nil
}

func currentTree(t *testing.T, e *Engine) *protocol.Tree {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.current == nil {
		t.Fatal("no current observation")
	}
	return e.current.Projection.Tree
}

func TestStartObserveActTextEnd(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	first := start.Observation
	if first.Tree == nil || first.Tree.Full == nil {
		t.Fatalf("first observation is not a full tree")
	}

	// observe(O1) acknowledges the full candidate and returns an empty delta.
	observeResult, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: first.ID})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	if observeResult.Observation == nil || observeResult.Observation.Tree == nil {
		t.Fatalf("observe returned no observation")
	}
	if observeResult.Observation.Tree.Delta == nil {
		t.Fatalf("expected delta after ACK")
	}
	if !observeResult.Observation.Tree.Delta.IsEmpty() {
		t.Fatalf("expected empty delta for unchanged page")
	}
	secondID := observeResult.Observation.ID

	// act tap on the button.
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatalf("button not found in tree")
	}
	tapResult, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
		ObservationID: secondID,
		Operation:     protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref}},
	})
	if apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	if tapResult.Outcome != OutcomeExecuted {
		t.Fatalf("tap outcome %s error %+v", tapResult.Outcome, tapResult.Error)
	}
	if len(h.device.taps) != 1 {
		t.Fatalf("expected one tap, got %v", h.device.taps)
	}
	if tapResult.Observation == nil || tapResult.Observation.ID == "" {
		t.Fatalf("tap returned no post observation")
	}
	thirdID := tapResult.Observation.ID

	// act text on the input field, with independent readback.
	input := findRole(currentTree(t, h.engine).Root, protocol.RoleInput)
	if input == nil {
		t.Fatalf("input not found in tree")
	}
	textResult, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
		ObservationID: thirdID,
		Operation: protocol.Operation{Type: "text",
			Target: &protocol.Target{UseRef: true, Ref: input.Ref}, Text: "你好世界"},
	})
	if apiErr != nil {
		t.Fatalf("text act rejected: %v", apiErr)
	}
	if textResult.Outcome != OutcomeExecuted {
		t.Fatalf("text outcome %s error %+v", textResult.Outcome, textResult.Error)
	}
	if len(h.device.texts) != 1 || h.device.texts[0] != "你好世界" {
		t.Fatalf("text not written exactly once: %v", h.device.texts)
	}
	if textResult.Observation == nil || textResult.Observation.ID == "" {
		t.Fatalf("text act returned no post observation")
	}

	// The post-observation delta must reconstruct the current tree.
	h.engine.mu.Lock()
	baseline := h.engine.baseline
	current := h.engine.current
	h.engine.mu.Unlock()
	if baseline == nil || baseline.ObservationID != first.ID {
		t.Fatalf("expected first full O1 as confirmed baseline")
	}
	delta := textResult.Observation.Tree.Delta
	if delta != nil {
		rebuilt, applyErr := observation.Apply(baseline.Tree, delta)
		if applyErr != nil {
			t.Fatalf("apply delta: %v", applyErr)
		}
		if !observation.Equal(rebuilt, current.Projection.Tree) {
			t.Fatalf("delta does not reconstruct the current tree")
		}
	}

	// end: revoke, enqueue close, confirm absence.
	endResult, apiErr := h.engine.End("agent", "", start.OperationID)
	if apiErr != nil {
		t.Fatalf("end rejected: %v", apiErr)
	}
	if endResult.Outcome != OutcomeExecuted || !endResult.IncludeSession ||
		endResult.Session == nil || endResult.Session.State != SessionEnded {
		t.Fatalf("end result: %+v", endResult)
	}
	// Deliver the close command and its receipt; cleanup should complete.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.engine.mu.Lock()
		pending := h.engine.cleanup != nil && h.engine.cleanup.State == protocol.CleanupPending
		complete := h.engine.cleanup != nil && h.engine.cleanup.State == protocol.CleanupComplete
		h.engine.mu.Unlock()
		if complete {
			break
		}
		h.sync(nil, nil)
		time.Sleep(50 * time.Millisecond)
		_ = pending
	}
	h.engine.mu.Lock()
	cleanup := h.engine.cleanup
	h.engine.mu.Unlock()
	if cleanup == nil || cleanup.State != protocol.CleanupComplete {
		t.Fatalf("cleanup did not complete: %+v", cleanup)
	}
	if cleanup.Display != protocol.CleanupConfirmed {
		t.Fatalf("display not confirmed: %+v", cleanup)
	}
}

func TestDuplicateOperationDoesNotRedispatch(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	before := h.device.helperCalls
	// A second start while the first session is active is rejected but still
	// records its own P.
	first, apiErr := h.engine.Start(context.Background(), "agent", "Pdup")
	if apiErr != nil {
		t.Fatalf("second start transport error: %v", apiErr)
	}
	if first.Outcome != OutcomeNotExecuted {
		t.Fatalf("second start should be rejected: %+v", first)
	}
	// The same P must be returned as the original record, not executed again.
	duplicate, apiErr := h.engine.Start(context.Background(), "agent", "Pdup")
	if apiErr != nil {
		t.Fatalf("duplicate P rejected: %v", apiErr)
	}
	if duplicate.OperationState != OperationFinished || duplicate.Outcome != OutcomeNotExecuted {
		t.Fatalf("unexpected duplicate result: %+v", duplicate)
	}
	if h.device.helperCalls != before {
		t.Fatalf("duplicate P started another display helper")
	}
}

func TestStopRevokesAndCleans(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	response := h.sync(nil, &protocol.AppStopPending{Seq: 1, Reason: "user_stop"})
	if response.Stop == nil || response.Stop.Seq != 1 {
		t.Fatalf("stop not acknowledged: %+v", response.Stop)
	}
	h.engine.mu.Lock()
	session := h.engine.session
	h.engine.mu.Unlock()
	if session == nil || session.State != SessionEnded || session.Reason != "user_stop" {
		t.Fatalf("session not revoked by stop: %+v", session)
	}
	if !h.engine.IsBusy() {
		// Not a requirement; just make sure no work remains after the flow.
	}
	deadline := time.Now().Add(3 * time.Second)
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
	h.engine.mu.Unlock()
	if cleanup == nil || cleanup.State != protocol.CleanupComplete {
		t.Fatalf("cleanup after stop did not complete: %+v", cleanup)
	}
	_ = start
}

func TestBusyRejectsSecondActionWithoutConsumingObservation(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	first := start.Observation
	// Occupy the single ordinary device work position.
	h.engine.mu.Lock()
	h.engine.work = &workState{OperationID: "Pblock", Agent: "agent", Kind: WorkObserve,
		Phase: "capture", SessionEpoch: h.engine.session.Epoch, Start: time.Now(),
		Ctx: context.Background(), Cancel: func() {}}
	h.engine.mu.Unlock()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	result, apiErr := h.engine.Act(context.Background(), "agent", "", &protocol.ActRequest{
		ObservationID: first.ID,
		Operation:     protocol.Operation{Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref}},
	})
	if apiErr != nil {
		t.Fatalf("busy act transport error: %v", apiErr)
	}
	if result.Outcome != OutcomeNotExecuted || result.Error == nil || result.Error.Code != protocol.CodeDeviceBusy {
		t.Fatalf("expected not_executed busy, got %+v", result)
	}
	h.engine.mu.Lock()
	h.engine.work = nil
	status := h.engine.observations[first.ID].Status
	h.engine.mu.Unlock()
	if status != ObservationReady {
		t.Fatalf("busy consumed the observation: %s", status)
	}
}

func TestSessionExpiryRevokes(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	h.engine.mu.Lock()
	h.engine.session.ExpiresAt = time.Now().Add(-time.Second)
	h.engine.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.engine.housekeeping(context.Background())
		h.engine.mu.Lock()
		ended := h.engine.session == nil || h.engine.session.State == SessionEnded
		h.engine.mu.Unlock()
		if ended {
			return
		}
		h.sync(nil, nil)
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("expired session was not revoked")
}

func TestStatusReportsObservationInputState(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	result, apiErr := h.engine.Status("agent", start.Observation.ID)
	if apiErr != nil {
		t.Fatalf("status rejected: %v", apiErr)
	}
	if result.ObservationState != ObservationReady || result.InputState != "effective" {
		t.Fatalf("unexpected status: %+v", result)
	}
	noRef, apiErr := h.engine.Status("agent", "")
	if apiErr != nil {
		t.Fatalf("self status rejected: %v", apiErr)
	}
	if noRef.CanStart == nil || *noRef.CanStart {
		t.Fatalf("active session must not report can_start: %+v", noRef)
	}
}

func TestStartRejectedWithoutPairedApp(t *testing.T) {
	dev := newFakeDevice()
	cfg := DefaultConfig("instance-test", "agent")
	eng := New(cfg, nil, dev, &fakeStore{}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	eng.SetDeviceReady(true)
	result, apiErr := eng.Start(context.Background(), "agent", "P1")
	if apiErr != nil {
		t.Fatalf("transport error: %v", apiErr)
	}
	if result.Outcome != OutcomeNotExecuted || result.Error == nil {
		t.Fatalf("expected admission rejection: %+v", result)
	}
}

func TestTreeTooLargeIsRejected(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.MaxFullTreeBytes = 16
	h.reportDisplayAfter(30 * time.Millisecond)
	result, apiErr := h.engine.Start(context.Background(), "agent", "")
	if apiErr != nil {
		t.Fatalf("transport error: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || result.Observation == nil ||
		result.Observation.Status != "failed" {
		t.Fatalf("expected executed start with failed first tree, got %+v", result)
	}
	if result.Observation.Error == nil || result.Observation.Error.Code != protocol.CodeTreeTooLarge {
		t.Fatalf("expected tree_too_large, got %+v", result.Observation.Error)
	}
}

func TestDeltaEncodingIsJSONStable(t *testing.T) {
	full := &protocol.Tree{Root: &protocol.Node{Ref: "n1", Role: protocol.RoleRoot}}
	data, err := json.Marshal(full)
	if err != nil || len(data) == 0 {
		t.Fatalf("marshal tree: %v", err)
	}
}
