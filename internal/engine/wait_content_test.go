package engine

import (
	"context"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func animatedWaitCapture(progress string, offset int) captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := f.captureLocked(display)
		if err != nil {
			return capture, err
		}
		bounds := device.Rect{Left: 100 + offset, Top: 500, Right: 400 + offset, Bottom: 600}
		index := len(capture.Nodes)
		capture.Nodes[0].Children = append(capture.Nodes[0].Children, index)
		capture.Nodes = append(capture.Nodes, device.RawNode{
			Index: index, Parent: 0, Depth: 1, DisplayID: display.ID, Visible: true,
			RawBounds: bounds, Bounds: bounds, Package: "com.example",
			ClassName: "android.widget.TextView", Text: progress, TextSet: true,
			HasEnabled: true, Enabled: true,
		})
		return capture, nil
	}
}

func TestWaitForContentStabilityWithAnimatedFrames(t *testing.T) {
	for _, tc := range []struct {
		name, query, trigger, state, stop string
		stable                            bool
	}{
		{"content", "", protocol.WaitTriggerStable, protocol.QualityStable, protocol.QualityStopStable, true},
		{"content_with_query", "0:03", protocol.WaitTriggerQuery, protocol.QualityStable, protocol.QualityStopStable, true},
		{"terminal_reuse", "absent marker", protocol.WaitTriggerTimeout, protocol.QualityStable, protocol.QualityStopStable, true},
		{"condition_only", "0:03", protocol.WaitTriggerQuery, protocol.QualityUnstable, protocol.QualityStopCondition, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, reference := waitForTestHarness(t)
			h.engine.cfg.Wait.MaxSamples = 3
			h.device.scriptCaptures(
				animatedWaitCapture("0:01", 0), animatedWaitCapture("0:02", 10),
				animatedWaitCapture("0:03", 20))
			before := h.device.captureCount()
			result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
				&protocol.WaitForRequest{Reference: reference, Timeout: 5,
					Stable: tc.stable, Query: tc.query, HasQuery: tc.query != "", Full: true})
			if apiErr != nil || result == nil || result.Error != nil {
				t.Fatalf("wait failed: result=%+v error=%v", result, apiErr)
			}
			if result.Wait == nil || result.Wait.Trigger != tc.trigger {
				t.Fatalf("wait = %+v, want trigger %s", result.Wait, tc.trigger)
			}
			if result.Observation == nil || result.Observation.Quality == nil {
				t.Fatal("wait did not publish an observation with quality")
			}
			quality := result.Observation.Quality
			if quality.State != tc.state || quality.Samples != 3 || quality.StopReason != tc.stop {
				t.Fatalf("quality = %+v, want %s/3/%s", quality, tc.state, tc.stop)
			}
			if reads := h.device.captureCount() - before; reads != 3 {
				t.Fatalf("read %d frames, want 3", reads)
			}
		})
	}
}

func TestContentDigestRetainsActionAndContainerText(t *testing.T) {
	value, hint := "before", "before"
	root := &protocol.Node{Ref: "n1", Role: protocol.RoleRoot, Children: []*protocol.Node{
		{Ref: "n2", Role: protocol.RoleButton, Label: []string{"disabled"}, Value: &value,
			Hint: &hint, Bounds: &protocol.Bounds{Left: 1, Right: 10}},
		{Ref: "n3", Role: protocol.RoleButton, Label: []string{"active"},
			Actions: []protocol.Action{protocol.ActionTap}},
		{Ref: "n4", Role: protocol.RoleGroup, Label: []string{"container"}, Children: []*protocol.Node{
			{Ref: "n5", Role: protocol.RoleText, Label: []string{"child"}},
		}},
	}}
	tree := &protocol.Tree{Root: root}
	capture := device.Capture{Foreground: device.Foreground{TaskID: intPtr(42)}}
	baseline, err := contentDigestOf(&capture, tree)
	if err != nil {
		t.Fatal(err)
	}
	if leaf := root.Children[0]; leaf.Ref != "n2" || leaf.Bounds == nil || len(leaf.Label) != 1 ||
		leaf.Value == nil || leaf.Hint == nil {
		t.Fatalf("digest modified the original leaf: %+v", leaf)
	}

	for _, tc := range []struct {
		name string
		edit func(*protocol.Node, *device.Capture)
		same bool
	}{
		{"passive button", func(root *protocol.Node, _ *device.Capture) {
			leaf := root.Children[0]
			leaf.Ref = "n20"
			leaf.Bounds.Left++
			leaf.Label[0] = "changed"
			*leaf.Value = "changed"
			*leaf.Hint = "changed"
		}, true},
		{"action text", func(root *protocol.Node, _ *device.Capture) {
			root.Children[1].Label[0] = "changed"
		}, false},
		{"container text", func(root *protocol.Node, _ *device.Capture) {
			root.Children[2].Label[0] = "changed"
		}, false},
		{"child removed", func(root *protocol.Node, _ *device.Capture) {
			root.Children[2].Children = nil
		}, false},
		{"foreground task", func(_ *protocol.Node, capture *device.Capture) {
			capture.Foreground.TaskID = intPtr(43)
		}, false},
		{"rotation", func(_ *protocol.Node, capture *device.Capture) {
			capture.Display.Rotation++
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := &protocol.Tree{Root: tree.Root.Clone()}
			candidateCapture := capture
			tc.edit(candidate.Root, &candidateCapture)
			got, err := contentDigestOf(&candidateCapture, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if same := got == baseline; same != tc.same {
				t.Fatalf("digest equality = %t, want %t", same, tc.same)
			}
		})
	}
}

func TestWaitForTerminalCaptureUsesSelectedDigest(t *testing.T) {
	for _, stable := range []bool{false, true} {
		capture := device.Capture{}
		tree := &protocol.Tree{Root: &protocol.Node{Role: protocol.RoleText, Label: []string{"0:02"}}}
		digest, err := waitDigestOf(&capture, tree, stable)
		if err != nil {
			t.Fatal(err)
		}
		run := &waitRun{samples: 2, consecutive: 2, lastDigest: digest, stopReason: protocol.QualityStopTimeout}
		terminal := &protocol.Tree{Root: tree.Root.Clone()}
		terminal.Root.Label[0] = "0:03"
		absorbWaitTerminalFrame(run, &treeAttempt{
			capture: capture, projection: &Projection{Tree: terminal},
		}, stable)
		quality := waitQualityOf(fastWaitPolicy(), run, waitFrameTerminalCapture)
		if stable {
			requireQuality(t, quality, protocol.QualityStable, 3, protocol.QualityStopStable)
		} else {
			requireQuality(t, quality, protocol.QualityUnstable, 3, protocol.QualityStopTimeout)
		}
	}
}

func TestWaitForTerminalDigestFailureBoundaries(t *testing.T) {
	// Invalid UTF-8 on an active node forces both digest encoders to fail. The
	// production tree encoding gate rejects this shape, so this focused test
	// intentionally constructs the terminal projection directly.
	tree := &protocol.Tree{Root: &protocol.Node{
		Role: protocol.RoleButton, Actions: []protocol.Action{protocol.ActionTap},
		Label: []string{"\xff"},
	}}
	for _, tc := range []struct {
		name        string
		stable      bool
		wantSamples int
	}{
		{"content digest failure does not count", true, 2},
		{"tree digest failure keeps legacy count", false, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &waitRun{
				samples: 2, consecutive: 2,
				stopReason: protocol.QualityStopTimeout,
			}
			absorbWaitTerminalFrame(run, &treeAttempt{projection: &Projection{Tree: tree}}, tc.stable)
			if run.samples != tc.wantSamples || run.consecutive != 0 {
				t.Fatalf("terminal failure counted incorrectly: samples=%d consecutive=%d",
					run.samples, run.consecutive)
			}
			quality := waitQualityOf(fastWaitPolicy(), run, waitFrameTerminalCapture)
			requireQuality(t, quality, protocol.QualityUnstable, tc.wantSamples,
				protocol.QualityStopTimeout)
		})
	}
}

func TestWaitForStableAttemptedFailureAtDeadlineClearsConsecutive(t *testing.T) {
	h, _ := waitForTestHarness(t)
	policy := fastWaitPolicy()
	policy.MinDwell = time.Millisecond
	policy.MaxSamples = 8
	h.engine.cfg.Wait = policy
	h.device.scriptCaptures(
		markerCapture("same"),
		markerCapture("same"),
		func(*fakeDevice, device.Display) (device.Capture, error) {
			time.Sleep(250 * time.Millisecond)
			return device.Capture{}, context.DeadlineExceeded
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	work := &workState{
		OperationID: "Pdeadline", Agent: "agent", Kind: WorkWaitFor,
		Phase: "wait_for.poll", Start: time.Now(), Projector: h.engine.projector,
		Ctx: ctx, Cancel: cancel,
	}
	h.engine.mu.Lock()
	work.SessionEpoch = h.engine.session.Epoch
	h.engine.work = work
	h.engine.mu.Unlock()
	defer h.engine.releaseWork(work)

	session := h.engine.sessionForWork(work)
	if session == nil {
		t.Fatal("deadline test work was not authorized")
	}
	run := &waitRun{stopReason: protocol.QualityStopNotSampled}
	trigger, candidate, apiErr := h.engine.pollWaitConditions(work, session,
		&protocol.WaitForRequest{Stable: true}, run,
		time.Now().Add(200*time.Millisecond))
	if apiErr != nil {
		t.Fatalf("wait poll failed: %+v", apiErr)
	}
	if trigger != protocol.WaitTriggerTimeout || candidate != nil {
		t.Fatalf("poll = trigger %q candidate %+v, want timeout without reuse", trigger, candidate)
	}
	if run.samples != 2 || run.consecutive != 0 {
		t.Fatalf("deadline failure retained stale stability: samples=%d consecutive=%d",
			run.samples, run.consecutive)
	}
}
