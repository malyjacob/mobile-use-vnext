package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

var textVerifyTestBounds = device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}

func newTextVerifyHarness(t *testing.T) (*harness, *textEvDevice, string, string) {
	t.Helper()
	h, dev, obsRef, ref := newTextEvidenceHarness(t, textVerifyTestBounds)
	h.device.scriptCaptures(textFlowBuild(0, textVerifyTestBounds, "", true))
	dev.setSteps = []textSetStep{{result: exactText("old")}}
	return h, dev, obsRef, ref
}

func textVerifyRead(value string) textGetStep {
	return textGetStep{result: device.TextResult{CandidateCount: 1, Value: strPtr(value)}}
}

func requireTextVerifyExecuted(t *testing.T, result *protocol.Result) {
	t.Helper()
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome=%q error=%+v, want executed", result.Outcome, result.Error)
	}
}

func requireTextVerifyUncertain(t *testing.T, result *protocol.Result, reason string, count int) {
	t.Helper()
	if result.Outcome != OutcomeUncertain || result.Error == nil || result.Error.Code != protocol.CodeTextResultUnknown {
		t.Fatalf("outcome=%q error=%+v, want text_result_uncertain", result.Outcome, result.Error)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "stage", "verify")
	requireTextDetail(t, details, "reason", reason)
	requireTextDetail(t, details, "candidate_count", count)
	requireTextDispatch(t, details, "not_attempted", "dispatched", "not_attempted")
}

func requireTextVerifyCalls(t *testing.T, dev *textEvDevice, reads, keys int) {
	t.Helper()
	tap, set, get, key := dev.counts()
	if tap != 0 || set != 1 || get != reads || key != keys {
		t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 0/1/%d/%d", tap, set, get, key, reads, keys)
	}
}

func TestTextVerifyAsyncFirstPollConfirms(t *testing.T) {
	sink := &syncBuffer{}
	log := eventlog.New(sink, "instance-test")
	t.Cleanup(log.Close)
	h := newHarnessWithLogger(t, true, log)
	dev := &textEvDevice{revalDevice: &revalDevice{fakeDevice: h.device}}
	h.engine.dev = dev
	h.device.scriptCaptures(textFlowBuild(0, textVerifyTestBounds, "", true))
	start := h.startSession()
	ref := currentTree(t, h.engine).Root.Children[0].Ref
	dev.setSteps = []textSetStep{{result: exactText("old")}}
	dev.getSteps = []textGetStep{textVerifyRead("new")}
	autoSubmit := false
	result := h.actWith(t, "PtextVerifyFirstPoll", textFlowRequest(start.Observation.ID, ref, "new", &autoSubmit))
	requireTextVerifyExecuted(t, result)
	requireTextVerifyCalls(t, dev, 1, 0)
	const event = "engine.text.verify"
	events := waitForEvent(t, sink, event)
	if strings.Count(events, event) != 1 {
		t.Fatalf("want a single verify event: %s", events)
	}
	_, line, _ := strings.Cut(events, event)
	line, _, _ = strings.Cut(line, "\n")
	for _, field := range []string{`operation_id="PtextVerifyFirstPoll"`, `mode="poll"`, "attempt=1", `outcome="executed"`, "count=1", "ms="} {
		if !strings.Contains(line, field) {
			t.Fatalf("verify event lacks %q: %s", field, line)
		}
	}
	t.Logf("verify event:%s, get=1", line)
}

func TestTextVerifyAsyncSecondPollConfirmsNilFirst(t *testing.T) {
	h, dev, obsRef, ref := newTextVerifyHarness(t)
	dev.setSteps[0].result.Value = nil
	dev.getSteps = []textGetStep{{result: device.TextResult{CandidateCount: 1}}, textVerifyRead("new")}
	autoSubmit := false
	result := h.actWith(t, "PtextVerifySecondPoll", textFlowRequest(obsRef, ref, "new", &autoSubmit))
	requireTextVerifyExecuted(t, result)
	requireTextVerifyCalls(t, dev, 2, 0)
}

func TestTextVerifyAsyncThenAutoSubmitEnter(t *testing.T) {
	h, dev, obsRef, ref := newTextVerifyHarness(t)
	dev.getSteps = []textGetStep{textVerifyRead("new"), textVerifyRead("new")}
	result := h.actWith(t, "PtextVerifyEnter", textFlowRequest(obsRef, ref, "new", nil))
	requireTextVerifyExecuted(t, result)
	requireTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputExecuted)
	requireTextVerifyCalls(t, dev, 2, 1)
}

func TestTextVerifyZeroCandidateRebinds(t *testing.T) {
	testTextVerifyZeroCandidateRebind(t, false)
}

func TestTextVerifyZeroCandidateRebindThenEnter(t *testing.T) {
	testTextVerifyZeroCandidateRebind(t, true)
}

func testTextVerifyZeroCandidateRebind(t *testing.T, autoSubmit bool) {
	t.Helper()
	h, dev, obsRef, ref := newTextVerifyHarness(t)
	grown := textVerifyTestBounds
	grown.Bottom += 60
	h.device.scriptCaptures(textFlowBuild(0, textVerifyTestBounds, "", true),
		textFlowBuild(0, grown, "new", true))
	dev.getSteps = []textGetStep{{result: device.TextResult{CandidateCount: 0}}, textVerifyRead("new")}
	reads, keys := 2, 0
	if autoSubmit {
		dev.getSteps = append(dev.getSteps, textVerifyRead("new"))
		reads, keys = 3, 1
	}
	result := h.actWith(t, "PtextVerifyRebind", textFlowRequest(obsRef, ref, "new", &autoSubmit))
	requireTextVerifyExecuted(t, result)
	requireTextVerifyCalls(t, dev, reads, keys)
	if autoSubmit {
		requireTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputExecuted)
	}
	targets := dev.recordedGetTextTargets()
	if targets[0].Bounds != textVerifyTestBounds {
		t.Fatalf("first get bounds = %+v, want %+v", targets[0].Bounds, textVerifyTestBounds)
	}
	for i, target := range targets[1:] {
		if target.Bounds != grown {
			t.Fatalf("get #%d bounds = %+v, want %+v", i+2, target.Bounds, grown)
		}
	}
}

func TestTextVerifyFormattedValueStaysMismatch(t *testing.T) {
	h, dev, obsRef, ref := newTextVerifyHarness(t)
	dev.setSteps[0].result = exactText("a b")
	dev.getSteps = []textGetStep{textVerifyRead("a b"), textVerifyRead("a b"), textVerifyRead("a b")}
	var setAt time.Time
	dev.setHook = func() { setAt = time.Now() }
	result := h.actWith(t, "PtextVerifyFormatted", textFlowRequest(obsRef, ref, "a\nb", nil))
	elapsed := time.Since(setAt)
	requireTextVerifyUncertain(t, result, "readback_mismatch", 1)
	if status := protocol.Status(result.Error.Code); status != 409 {
		t.Fatalf("protocol status = %d, want 409", status)
	}
	requireTextVerifyCalls(t, dev, 3, 0)
	t.Logf("set callback to result: %s, get=3", elapsed)
}

func TestTextVerifyMultiCandidateStopsImmediately(t *testing.T) {
	h, dev, obsRef, ref := newTextVerifyHarness(t)
	dev.getSteps = []textGetStep{{result: device.TextResult{CandidateCount: 2}}, textVerifyRead("new")}
	result := h.actWith(t, "PtextVerifyMulti", textFlowRequest(obsRef, ref, "new", nil))
	requireTextVerifyUncertain(t, result, "readback_unavailable", 2)
	requireTextVerifyCalls(t, dev, 1, 0)
}

// cancelOnVerifyWait triggers cancellation when the backoff subscribes to Done,
// so the test does not race a real timer against the first GetText call.
type cancelOnVerifyWait struct {
	context.Context
	cancel context.CancelFunc
}

func (c cancelOnVerifyWait) Done() <-chan struct{} {
	c.cancel()
	return c.Context.Done()
}

func TestTextVerifyStopDuringBackoff(t *testing.T) {
	h, dev, obsRef, ref := newTextVerifyHarness(t)
	dev.getSteps = []textGetStep{textVerifyRead("new")}
	var setAt time.Time
	dev.setHook = func() {
		h.engine.mu.Lock()
		defer h.engine.mu.Unlock()
		work := h.engine.work
		work.Ctx = cancelOnVerifyWait{Context: work.Ctx, cancel: work.Cancel}
		setAt = time.Now()
	}
	result := h.actWith(t, "PtextVerifyStop", textFlowRequest(obsRef, ref, "new", nil))
	elapsed := time.Since(setAt)
	if result.Outcome != OutcomeUncertain || result.Error == nil || result.Error.Code != protocol.CodeStopped {
		t.Fatalf("outcome=%q error=%+v, want control_revoked/uncertain", result.Outcome, result.Error)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "stage", "verify")
	requireTextDetail(t, details, "reason", "authorization_revoked")
	requireTextDispatch(t, details, "not_attempted", "dispatched", "not_attempted")
	requireTextInput(t, result, protocol.TextInputUncertain, protocol.TextInputNotExecuted)
	requireTextVerifyCalls(t, dev, 0, 0)
	t.Logf("set callback to result: %s, get=0 key=0", elapsed)
}

func TestTextVerifyRebindNotUniqueStaysUncertain(t *testing.T) {
	h, rec := newRevalHarness(t)
	dev := &textEvDevice{revalDevice: rec}
	h.engine.dev = dev
	original := func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return revalCapture(display, revalRoot(display.ID, 1),
			revalEditText(1, 0, 1, display.ID, textVerifyTestBounds, "old", "", "", true)), nil
	}
	h.device.scriptCaptures(original)
	start := h.startSession()
	ref := currentTree(t, h.engine).Root.Children[0].Ref
	h.device.scriptCaptures(original, func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		grown := textVerifyTestBounds
		grown.Bottom += 60
		other := device.Rect{Left: 100, Top: 500, Right: 900, Bottom: 600}
		return revalCapture(display, revalRoot(display.ID, 1, 2),
			revalEditText(1, 0, 1, display.ID, grown, "new", "", "", true),
			revalEditText(2, 0, 1, display.ID, other, "new", "", "", true)), nil
	})
	dev.setSteps = []textSetStep{{result: exactText("old")}}
	dev.getSteps = []textGetStep{{result: device.TextResult{CandidateCount: 0}}}
	autoSubmit := false
	result := h.actWith(t, "PtextVerifyAmbiguous", textFlowRequest(start.Observation.ID, ref, "new", &autoSubmit))
	requireTextVerifyUncertain(t, result, "readback_unavailable", 0)
	requireTextVerifyCalls(t, dev, 1, 0)
}

func TestTextVerifySyncFastPathKeepsZeroReads(t *testing.T) {
	h, dev, obsRef, ref := newTextVerifyHarness(t)
	dev.setSteps[0].result = exactText("new")
	autoSubmit := false
	result := h.actWith(t, "PtextVerifyDirect", textFlowRequest(obsRef, ref, "new", &autoSubmit))
	requireTextVerifyExecuted(t, result)
	requireTextVerifyCalls(t, dev, 0, 0)
}
