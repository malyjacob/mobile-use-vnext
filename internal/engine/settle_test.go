package engine

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// settleHarness builds a harness whose request path does not settle, so a test
// drives the convergence loop itself and controls its timing exactly.
func settleHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.engine.cfg.Settle = SettlePolicies{}
	return h
}

// settleWork takes the single work position of an active session so a test can
// call the acquisition steps directly.
func (h *harness) settleWork(t *testing.T, p string) (*workState, *sessionState) {
	t.Helper()
	op, duplicate, apiErr := h.engine.registerOperation(p, "agent", "act")
	if apiErr != nil {
		t.Fatalf("register operation rejected: %v", apiErr)
	}
	if duplicate != nil {
		t.Fatalf("operation %q is not unique", p)
	}
	work, busy := h.engine.acquireWork(op, WorkAct)
	if busy != nil {
		t.Fatalf("work position rejected: %v", busy)
	}
	t.Cleanup(func() { h.engine.releaseWork(work) })
	session := h.engine.sessionForWork(work)
	if session == nil {
		t.Fatal("no session snapshot for the work position")
	}
	return work, session
}

func rawButtonLabel(capture *device.Capture) string {
	for index := range capture.Nodes {
		if capture.Nodes[index].ClassName == "android.widget.Button" {
			return capture.Nodes[index].Text
		}
	}
	return ""
}

func treeHasLabel(root *protocol.Node, want string) bool {
	if root == nil {
		return false
	}
	for _, label := range root.Label {
		if label == want {
			return true
		}
	}
	for _, child := range root.Children {
		if treeHasLabel(child, want) {
			return true
		}
	}
	return false
}

func TestSettlePublishesLaterFrameOfEqualPair(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Psettlepair")
	h.device.scriptCaptures(markerCapture("A"), markerCapture("B"), markerCapture("B"))

	run, apiErr := h.engine.settleCapture(work.Ctx, work, session, "act", SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 5, MaxBudget: time.Second,
	})
	if apiErr != nil {
		t.Fatalf("settle failed: %v", apiErr)
	}
	if run.samples != 3 || !run.stable {
		t.Fatalf("run = %d samples, stable=%v; want 3 and stable", run.samples, run.stable)
	}
	if label := rawButtonLabel(&run.frame.capture); label != "B" {
		t.Fatalf("published frame label = %q, want the later frame", label)
	}
}

func TestSettleBudgetExhaustionPublishesNewestFrame(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Psettlebudget")
	h.device.scriptCaptures(
		markerCapture("A"), markerCapture("B"), markerCapture("C"), markerCapture("D"))

	run, apiErr := h.engine.settleCapture(work.Ctx, work, session, "act", SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 4, MaxBudget: time.Second,
	})
	if apiErr != nil {
		t.Fatalf("settle failed: %v", apiErr)
	}
	if run.samples != 4 || run.stable {
		t.Fatalf("run = %d samples, stable=%v; want 4 and unstable", run.samples, run.stable)
	}
	if label := rawButtonLabel(&run.frame.capture); label != "D" {
		t.Fatalf("published frame label = %q, want the newest frame", label)
	}
}

func TestSettleQuietWindowDelaysTheComparingSample(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Psettledwell")
	h.device.scriptCaptures(markerCapture("A"), markerCapture("A"))

	begin := time.Now()
	run, apiErr := h.engine.settleCapture(work.Ctx, work, session, "act", SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 5,
		MinDwell: 80 * time.Millisecond, MaxBudget: time.Second,
	})
	if apiErr != nil {
		t.Fatalf("settle failed: %v", apiErr)
	}
	if elapsed := time.Since(begin); elapsed < 70*time.Millisecond {
		t.Fatalf("settle took %s, want at least one quiet window", elapsed)
	}
	if run.samples != 2 || !run.stable {
		t.Fatalf("run = %d samples, stable=%v; want 2 and stable", run.samples, run.stable)
	}
	// The published frame is the later one, never the first frame re-sent after
	// the wait.
	if !run.frame.capture.CapturedAt.After(begin.Add(70 * time.Millisecond)) {
		t.Fatalf("published frame was captured at %s, before the quiet window closed",
			run.frame.capture.CapturedAt.Sub(begin))
	}
}

func TestSettleMinSamplesRequiresAnotherFrame(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Psettlemin")
	h.device.scriptCaptures(markerCapture("A"), markerCapture("A"), markerCapture("A"))

	run, apiErr := h.engine.settleCapture(work.Ctx, work, session, "act", SettlePolicy{
		Enabled: true, MinSamples: 3, MaxSamples: 5, MaxBudget: time.Second,
	})
	if apiErr != nil {
		t.Fatalf("settle failed: %v", apiErr)
	}
	if run.samples != 3 || !run.stable {
		t.Fatalf("run = %d samples, stable=%v; want the third frame to confirm",
			run.samples, run.stable)
	}
}

func TestSettleUnreadableSampleKeepsPreviousFrame(t *testing.T) {
	h := settleHarness(t)
	h.startSession()
	work, session := h.settleWork(t, "Psettlefail")
	h.device.scriptCaptures(markerCapture("A"), failingCapture())

	run, apiErr := h.engine.settleCapture(work.Ctx, work, session, "act", SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 5, MaxBudget: time.Second,
	})
	if apiErr != nil {
		t.Fatalf("an unreadable sample must not fail the acquisition: %v", apiErr)
	}
	if run.samples != 1 || run.stable {
		t.Fatalf("run = %d samples, stable=%v; want the first frame and unstable",
			run.samples, run.stable)
	}
	if label := rawButtonLabel(&run.frame.capture); label != "A" {
		t.Fatalf("published frame label = %q, want the frame before the failure", label)
	}
}

func TestSettleStopsWithoutObservation(t *testing.T) {
	h := settleHarness(t)
	start := h.startSession()
	work, session := h.settleWork(t, "Psettlestop")
	h.device.scriptCaptures(markerCapture("A"), markerCapture("B"))

	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = h.engine.End("agent", "Psettleend", start.OperationID)
	}()
	run, apiErr := h.engine.settleCapture(work.Ctx, work, session, "act", SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 5,
		MinDwell: 400 * time.Millisecond, MaxBudget: time.Second,
	})
	if apiErr == nil || apiErr.Code != protocol.CodeStopped {
		t.Fatalf("settle error = %+v, want stopped", apiErr)
	}
	if run != nil {
		t.Fatalf("a revoked settle returned a frame: %+v", run.frame)
	}
}

func TestSettlePolicyIsSelectedByPhase(t *testing.T) {
	h := settleHarness(t)
	want := SettlePolicies{
		Act:     SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 5, MinDwell: 400 * time.Millisecond, MaxBudget: 2 * time.Second},
		Start:   SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 4, MinDwell: 300 * time.Millisecond, MaxBudget: 1500 * time.Millisecond},
		OpenApp: SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 5, MinDwell: 600 * time.Millisecond, MaxBudget: 3 * time.Second},
	}
	h.engine.cfg.Settle = want
	for phase, expected := range map[string]SettlePolicy{
		"act": want.Act, "start": want.Start, "open_app": want.OpenApp,
	} {
		if got := h.engine.settlePolicy(phase); got != expected {
			t.Fatalf("%s policy = %+v, want %+v", phase, got, expected)
		}
	}
	for _, phase := range []string{"observe", "shell"} {
		if policy := h.engine.settlePolicy(phase); policy.Enabled {
			t.Fatalf("%s must not settle", phase)
		}
	}
}

func TestSettleUsesStartTierForFirstObservation(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Start: SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 3,
		MinDwell: 80 * time.Millisecond, MaxBudget: time.Second,
	}}
	begin := time.Now()
	start := h.startSession()
	if elapsed := time.Since(begin); elapsed < 70*time.Millisecond {
		t.Fatalf("start took %s, want the start quiet window", elapsed)
	}
	if start.Observation == nil || start.Observation.ID == "" {
		t.Fatalf("start returned no first observation: %+v", start.Observation)
	}
}

func TestSettleUsesOpenAppTierForLaunchConfirmation(t *testing.T) {
	h := newHarness(t)
	h.engine.cfg.Settle = SettlePolicies{OpenApp: SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 3,
		MinDwell: 80 * time.Millisecond, MaxBudget: time.Second,
	}}
	first := h.startSession()
	h.device.launchTask = &device.Task{TaskID: 77, Package: "com.example.app"}
	begin := time.Now()
	result, apiErr := h.engine.OpenApp(context.Background(), "agent", "Papp",
		openAppRequest(first.Observation, "com.example.app"))
	if apiErr != nil {
		t.Fatalf("OpenApp: %v", apiErr)
	}
	if elapsed := time.Since(begin); elapsed < 70*time.Millisecond {
		t.Fatalf("open_app took %s, want the open_app quiet window", elapsed)
	}
	if result.Observation == nil || result.Observation.Tree == nil || result.Observation.Tree.Full == nil {
		t.Fatalf("expected a full post observation: %+v", result.Observation)
	}
}

// TestSettleReadsImageAfterConvergence proves the acquisition order that keeps
// the fixed image/tree span: settle samples first, then the image read, then one
// tree read that refreshes the published frame.
func TestSettleReadsImageAfterConvergence(t *testing.T) {
	h, _ := imageTestHarness(t)
	h.engine.cfg.Settle = SettlePolicies{Act: SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 5,
		MinDwell: 40 * time.Millisecond, MaxBudget: time.Second,
	}}
	start := h.startSession()
	setFakeImageSource(h, time.Now())
	h.device.readTrace()

	result, apiErr := h.engine.Act(context.Background(), "agent", "Psettleorder",
		tapButtonRequest(t, h, start.Observation.ID))
	if apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	if result.Observation == nil || result.Observation.Image == nil ||
		result.Observation.Image.Error != nil {
		t.Fatalf("act returned no usable image: %+v", result.Observation)
	}
	trace := h.device.readTrace()
	imageAt := -1
	for index, event := range trace {
		if event == "image" {
			imageAt = index
			break
		}
	}
	if imageAt < 0 {
		t.Fatalf("no image read in %v", trace)
	}
	// One revalidation read plus the two converging samples come before the
	// image, and exactly one refresh read follows it.
	if imageAt < 3 {
		t.Fatalf("image was read before the settle samples: %v", trace)
	}
	if imageAt != len(trace)-2 {
		t.Fatalf("expected exactly one tree read after the image: %v", trace)
	}
}

func TestSettlePolicyValidation(t *testing.T) {
	valid := SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 4, MinDwell: 100 * time.Millisecond, MaxBudget: time.Second}
	cases := map[string]SettlePolicies{
		"max samples below two":   {Act: SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 1, MaxBudget: time.Second}},
		"min samples below two":   {Act: SettlePolicy{Enabled: true, MinSamples: 1, MaxSamples: 4, MaxBudget: time.Second}},
		"min samples above max":   {Act: SettlePolicy{Enabled: true, MinSamples: 5, MaxSamples: 4, MaxBudget: time.Second}},
		"dwell above budget":      {Act: SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 4, MinDwell: 2 * time.Second, MaxBudget: time.Second}},
		"budget above limit":      {OpenApp: SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 5, MaxBudget: 4 * time.Second}},
		"disabled tier is exempt": {Act: SettlePolicy{}},
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("the documented policy was rejected: %v", err)
	}
	for name, policies := range cases {
		wantRejected := name != "disabled tier is exempt"
		if err := policies.Validate(); (err != nil) != wantRejected {
			t.Fatalf("%s: validate error = %v, want rejected=%v", name, err, wantRejected)
		}
	}
	if err := DefaultSettlePolicies().Validate(); err != nil {
		t.Fatalf("the shipped policy was rejected: %v", err)
	}
}

// TestSettleBudgetFitsWorkWindow keeps one convergence wait a small fraction of
// the operation it belongs to, so a settle run can never dominate the phase.
func TestSettleBudgetFitsWorkWindow(t *testing.T) {
	cfg := DefaultConfig("instance-test", "agent")
	startWindow := cfg.StartTimeout + cfg.WorkTimeout
	actionWindow := 2*cfg.WorkTimeout + 10*time.Second
	for name, entry := range map[string]struct {
		policy SettlePolicy
		window time.Duration
	}{
		"start":    {cfg.Settle.Start, startWindow},
		"act":      {cfg.Settle.Act, actionWindow},
		"open_app": {cfg.Settle.OpenApp, actionWindow},
	} {
		if entry.policy.MaxBudget*10 > entry.window {
			t.Fatalf("%s budget %s exceeds a tenth of its work window %s",
				name, entry.policy.MaxBudget, entry.window)
		}
	}
}

// TestIdentityFieldCountMatchesDigest guards the one maintenance hazard of the
// hand-written digest: a new identity field must extend the digest too.
func TestIdentityFieldCountMatchesDigest(t *testing.T) {
	if fields := reflect.TypeOf(rawIdentity{}).NumField(); fields != rawIdentityFieldCount {
		t.Fatalf("rawIdentity has %d fields, digest writes %d; extend identityFingerprint",
			fields, rawIdentityFieldCount)
	}
}

// TestSettleDigestFollowsThePublicTree proves L2 covers what L1 cannot: a
// visibility flip does not change any node identity but does change the public
// tree the agent reads.
func TestSettleDigestFollowsThePublicTree(t *testing.T) {
	builder := observation.NewBuilder()
	digestOf := func(capture *device.Capture) sampleDigest {
		t.Helper()
		projection, err := builder.Preview(capture)
		if err != nil {
			t.Fatalf("preview failed: %v", err)
		}
		digest, err := sampleDigestOf(capture, projection.Tree)
		if err != nil {
			t.Fatalf("digest failed: %v", err)
		}
		return digest
	}
	page := func(visible bool) *device.Capture {
		bounds := device.Rect{Left: 10, Top: 10, Right: 110, Bottom: 60}
		nodes := []device.RawNode{
			{
				Index: 0, Parent: -1, Children: []int{1}, Depth: 0, Visible: true,
				RawBounds: device.Rect{Right: 1080, Bottom: 2400}, Bounds: device.Rect{Right: 1080, Bottom: 2400},
				Package: "com.example", ClassName: "android.widget.FrameLayout",
				HasEnabled: true, Enabled: true,
			},
			{
				Index: 1, Parent: 0, Depth: 1, Visible: visible,
				RawBounds: bounds, Bounds: bounds,
				Package: "com.example", ClassName: "android.widget.TextView",
				Text: "Label", TextSet: true,
			},
		}
		return &device.Capture{
			Display: device.Display{Width: 1080, Height: 2400}, Nodes: nodes, Root: 0,
		}
	}
	shown, hidden := page(true), page(false)
	if hierarchyDigest(shown) != hierarchyDigest(hidden) {
		t.Fatal("a visibility flip must not change the raw identity chain")
	}
	if digestOf(shown).tree == digestOf(hidden).tree {
		t.Fatal("a visibility flip must change the public tree digest")
	}
	if digestOf(shown) == digestOf(hidden) {
		t.Fatal("a visibility flip must not settle")
	}
}

// TestSettleKeepsObservationShape proves the convergence work stays invisible
// in the public result.
func TestSettleKeepsObservationShape(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	result, apiErr := h.engine.Act(context.Background(), "agent", "Psettleshape", &protocol.ActRequest{
		ObservationID: start.Observation.ID,
		Operation: protocol.Operation{
			Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref},
		},
	})
	if apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	if result.Observation == nil || result.Observation.ID == "" {
		t.Fatalf("act returned no post observation: %+v", result.Observation)
	}
	encoded, err := json.Marshal(result.Observation)
	if err != nil {
		t.Fatalf("encode observation: %v", err)
	}
	for _, field := range []string{`"stable"`, `"samples"`, `"settle"`} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("observation carries settle metadata %s: %s", field, encoded)
		}
	}
}

// TestSettleRecordsConvergenceFacts proves the convergence facts are recorded
// through the field whitelist, so an operator can calibrate the policies.
func TestSettleRecordsConvergenceFacts(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	h.engine.cfg.Settle = SettlePolicies{Act: SettlePolicy{
		Enabled: true, MinSamples: 2, MaxSamples: 4, MaxBudget: time.Second,
	}}
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	if _, apiErr := h.engine.Act(context.Background(), "agent", "Psettlelog", &protocol.ActRequest{
		ObservationID: start.Observation.ID,
		Operation: protocol.Operation{
			Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref},
		},
	}); apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	logger.Close()
	recorded := buffer.String()
	for _, want := range []string{"observation.settle", "samples=2", "stable=true"} {
		if !strings.Contains(recorded, want) {
			t.Fatalf("event log lacks %q: %s", want, recorded)
		}
	}
}
