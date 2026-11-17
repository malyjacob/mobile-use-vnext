package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// textEvDevice scripts the three text primitives and keeps exact call counts.
type textEvDevice struct {
	*revalDevice
	mu sync.Mutex

	tapReceipt *device.DispatchReceipt
	tapErr     error
	tapHook    func()
	tapCalls   int

	setSteps []textSetStep
	setHook  func()
	setCalls int

	getSteps []textGetStep
	getCalls int

	keySteps []textKeyStep
	keyHook  func()
	keyCalls int

	taps [][3]int
}

type textSetStep struct {
	result device.TextResult
	err    error
}

type textGetStep struct {
	result device.TextResult
	err    error
}

type textKeyStep struct {
	receipt device.DispatchReceipt
	err     error
}

func (d *textEvDevice) Tap(ctx context.Context, displayID, x, y int) (device.DispatchReceipt, error) {
	d.mu.Lock()
	d.tapCalls++
	d.taps = append(d.taps, [3]int{displayID, x, y})
	hook, receipt, err := d.tapHook, d.tapReceipt, d.tapErr
	d.mu.Unlock()
	if hook != nil {
		hook()
	}
	if receipt != nil {
		return *receipt, err
	}
	return d.revalDevice.Tap(ctx, displayID, x, y)
}

func (d *textEvDevice) SetTextOnDisplay(ctx context.Context, target device.TextTarget, text string) (device.TextResult, error) {
	d.mu.Lock()
	d.setCalls++
	targets := &d.revalDevice.setTextTargets
	var step textSetStep
	scripted := len(d.setSteps) > 0
	if scripted {
		step = d.setSteps[0]
		if len(d.setSteps) > 1 {
			d.setSteps = d.setSteps[1:]
		}
	}
	hook := d.setHook
	d.mu.Unlock()
	d.revalDevice.recMu.Lock()
	*targets = append(*targets, recordedSetText{target: target, text: text})
	d.revalDevice.recMu.Unlock()
	if hook != nil {
		hook()
	}
	if scripted {
		return step.result, step.err
	}
	return d.revalDevice.fakeDevice.SetTextOnDisplay(ctx, target, text)
}

func (d *textEvDevice) GetTextOnDisplay(ctx context.Context, target device.TextTarget) (device.TextResult, error) {
	d.mu.Lock()
	d.getCalls++
	var step textGetStep
	scripted := len(d.getSteps) > 0
	if scripted {
		step = d.getSteps[0]
		if len(d.getSteps) > 1 {
			d.getSteps = d.getSteps[1:]
		}
	}
	d.mu.Unlock()
	d.revalDevice.recMu.Lock()
	d.revalDevice.getTextTargets = append(d.revalDevice.getTextTargets, target)
	d.revalDevice.recMu.Unlock()
	if scripted {
		return step.result, step.err
	}
	return d.revalDevice.fakeDevice.GetTextOnDisplay(ctx, target)
}

func (d *textEvDevice) Key(ctx context.Context, displayID int, key string) (device.DispatchReceipt, error) {
	d.mu.Lock()
	d.keyCalls++
	var step textKeyStep
	scripted := len(d.keySteps) > 0
	if scripted {
		step = d.keySteps[0]
		if len(d.keySteps) > 1 {
			d.keySteps = d.keySteps[1:]
		}
	}
	hook := d.keyHook
	d.mu.Unlock()
	if hook != nil {
		hook()
	}
	if scripted {
		return step.receipt, step.err
	}
	return d.revalDevice.Key(ctx, displayID, key)
}

func (d *textEvDevice) counts() (tap, set, get, key int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tapCalls, d.setCalls, d.getCalls, d.keyCalls
}

func newTextEvidenceHarness(t *testing.T, bounds device.Rect) (*harness, *textEvDevice, string, string) {
	t.Helper()
	h, rec := newRevalHarness(t)
	dev := &textEvDevice{revalDevice: rec}
	h.engine.dev = dev
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false))
	start := h.startSession()
	input := currentTree(t, h.engine).Root.Children[0]
	if input == nil {
		t.Fatal("input node not found")
	}
	return h, dev, start.Observation.ID, input.Ref
}

func textDetailsOf(t *testing.T, result *protocol.Result) map[string]any {
	t.Helper()
	if result == nil || result.Error == nil {
		t.Fatalf("result has no error: %+v", result)
	}
	if result.Error.Details == nil {
		t.Fatalf("error carries no details: %+v", result.Error)
	}
	return result.Error.Details
}

func textDispatchOf(t *testing.T, details map[string]any) map[string]any {
	t.Helper()
	raw, ok := details["dispatch"]
	if !ok {
		t.Fatalf("details lack dispatch: %+v", details)
	}
	dispatch, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("dispatch is not an object: %#v", raw)
	}
	return dispatch
}

func requireTextDetail(t *testing.T, details map[string]any, key string, want any) {
	t.Helper()
	if got, ok := details[key]; !ok || got != want {
		t.Fatalf("details[%q] = %#v (present=%t), want %#v (%+v)", key, got, ok, want, details)
	}
}

func requireTextDispatch(t *testing.T, details map[string]any, focus, write, enter string) {
	t.Helper()
	dispatch := textDispatchOf(t, details)
	if dispatch["focus"] != focus || dispatch["write"] != write || dispatch["enter"] != enter {
		t.Fatalf("dispatch = %+v, want focus=%s write=%s enter=%s", dispatch, focus, write, enter)
	}
}

func exactText(value string) device.TextResult {
	return device.TextResult{CandidateCount: 1, Value: &value, Issued: true, Accepted: true,
		Receipt: device.DispatchTerminated}
}

func exactTextNoAccept(value string) device.TextResult {
	return device.TextResult{CandidateCount: 1, Value: &value, Issued: true,
		Receipt: device.DispatchTerminated}
}

// TestTextFocusedRefSkipsFocusAndAcceptsSetReadback proves the fast path: an
// already-focused ref is bound from the fresh frame, the exact refreshed value
// from the single set call completes the write without another tree read or
// GetText, and accepted is not an independent veto.
func TestTextFocusedRefSkipsFocusAndAcceptsSetReadback(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", true))
	dev.setSteps = []textSetStep{{result: exactTextNoAccept("hello")}}

	falseValue := false
	result := h.actWith(t, "PtextFast", textFlowRequest(obsRef, ref, "hello", &falseValue))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("focused ref text outcome = %q error=%+v", result.Outcome, result.Error)
	}
	if result.TextInput != nil {
		t.Fatalf("press_enter=false returned text_input: %+v", result.TextInput)
	}
	tap, set, get, key := dev.counts()
	if tap != 0 || set != 1 || get != 0 || key != 0 {
		t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 0/1/0/0",
			tap, set, get, key)
	}
}

// TestTextSetNoUniqueTargetReportsUndispatchedFacts proves a complete set
// response that never called the write primitive is not a write attempt. With
// an already-focused ref the overall result stays not_executed and the details
// explain the write stage without a second device read.
func TestTextSetNoUniqueTargetReportsUndispatchedFacts(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", true))
	dev.setSteps = []textSetStep{{result: device.TextResult{
		CandidateCount: 0, Receipt: device.DispatchTerminated,
	}}}

	falseValue := false
	result := h.actWith(t, "PtextNoUnique", textFlowRequest(obsRef, ref, "hello", &falseValue))
	if result.Outcome != OutcomeNotExecuted {
		t.Fatalf("no unique target with no other primitive outcome = %q, want not_executed (%+v)",
			result.Outcome, result)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeTextTargetUnknown {
		t.Fatalf("error = %+v, want text_target_uncertain", result.Error)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "stage", "write")
	requireTextDetail(t, details, "reason", "no_unique_target")
	requireTextDetail(t, details, "candidate_count", 0)
	requireTextDispatch(t, details, "not_attempted", "not_dispatched", "not_attempted")
	tap, set, get, key := dev.counts()
	if tap != 0 || set != 1 || get != 0 || key != 0 {
		t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d", tap, set, get, key)
	}
}

// TestTextReadbackMismatchAndUnavailable proves a dispatched write with an
// unusable exact readback is uncertain at the verify stage, never retried, and
// never followed by ENTER.
func TestTextReadbackMismatchAndUnavailable(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	cases := []struct {
		name   string
		result device.TextResult
		reason string
	}{
		{
			name: "mismatch",
			result: device.TextResult{CandidateCount: 1, Value: strPtr("other"), Issued: true,
				Receipt: device.DispatchTerminated},
			reason: "readback_mismatch",
		},
		{
			name: "unavailable",
			result: device.TextResult{CandidateCount: 1, Value: nil, Issued: true,
				Receipt: device.DispatchTerminated},
			reason: "readback_unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
			h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
				textFlowBuild(0, bounds, "", true))
			dev.setSteps = []textSetStep{{result: tc.result}}
			result := h.actWith(t, "PtextVerify", textFlowRequest(obsRef, ref, "hello", nil))
			if result.Outcome != OutcomeUncertain {
				t.Fatalf("outcome = %q, want uncertain (%+v)", result.Outcome, result)
			}
			if result.Error == nil || result.Error.Code != protocol.CodeTextResultUnknown {
				t.Fatalf("error = %+v, want text_result_uncertain", result.Error)
			}
			requireTextInput(t, result, protocol.TextInputUncertain, protocol.TextInputNotExecuted)
			details := textDetailsOf(t, result)
			requireTextDetail(t, details, "stage", "verify")
			requireTextDetail(t, details, "reason", tc.reason)
			requireTextDetail(t, details, "candidate_count", 1)
			requireTextDispatch(t, details, "dispatched", "dispatched", "not_attempted")
			tap, set, get, key := dev.counts()
			if tap != 1 || set != 1 || get != 0 || key != 0 {
				t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 1/1/0/0",
					tap, set, get, key)
			}
		})
	}
}

// TestTextMalformedSetResultIsAnUnknownReceipt proves an invalid set value is
// never classified as a definite not-dispatched write.
func TestTextMalformedSetResultIsAnUnknownReceipt(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true))
	dev.setSteps = []textSetStep{{
		result: device.TextResult{Receipt: device.DispatchUnknown},
		err:    device.ErrTextResultInvalid,
	}}
	result := h.actWith(t, "PtextMalformed", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("outcome = %q, want uncertain (%+v)", result.Outcome, result)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeTextResultUnknown {
		t.Fatalf("error = %+v, want text_result_uncertain", result.Error)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "stage", "write")
	requireTextDetail(t, details, "reason", "rpc_result_invalid")
	requireTextDispatch(t, details, "dispatched", "unknown", "not_attempted")
	if _, present := details["candidate_count"]; present {
		t.Fatalf("malformed result invented a candidate count: %+v", details)
	}
}

// TestTextEnterGateRequiresFreshExactValue proves the independent ENTER probe
// never dispatches ENTER when the target value no longer matches, and keeps the
// already-confirmed write.
func TestTextEnterGateRequiresFreshExactValue(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	t.Run("value_changed", func(t *testing.T) {
		h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
		h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
			textFlowBuild(0, bounds, "", true))
		dev.setSteps = []textSetStep{{result: exactText("hello")}}
		changed := "hello!"
		dev.getSteps = []textGetStep{{result: device.TextResult{CandidateCount: 1, Value: &changed}}}
		result := h.actWith(t, "PtextEnterValue", textFlowRequest(obsRef, ref, "hello", nil))
		if result.Outcome != OutcomeUncertain {
			t.Fatalf("outcome = %q, want uncertain (%+v)", result.Outcome, result)
		}
		requireTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputNotExecuted)
		details := textDetailsOf(t, result)
		requireTextDetail(t, details, "stage", "enter")
		requireTextDetail(t, details, "reason", "enter_target_unconfirmed")
		requireTextDispatch(t, details, "dispatched", "dispatched", "not_attempted")
		tap, set, get, key := dev.counts()
		if tap != 1 || set != 1 || get != 1 || key != 0 {
			t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 1/1/1/0",
				tap, set, get, key)
		}
	})

	t.Run("foreground_changed", func(t *testing.T) {
		h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
		h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
			textFlowBuild(0, bounds, "", true))
		dev.setSteps = []textSetStep{{result: exactText("hello")}}
		otherTask := 99
		h.device.foreground = &device.Foreground{
			State: device.ForegroundPresent, Package: "com.other", TaskID: &otherTask,
		}
		result := h.actWith(t, "PtextEnterContext", textFlowRequest(obsRef, ref, "hello", nil))
		if result.Outcome != OutcomeUncertain {
			t.Fatalf("outcome = %q, want uncertain (%+v)", result.Outcome, result)
		}
		requireTextInput(t, result, protocol.TextInputExecuted, protocol.TextInputNotExecuted)
		details := textDetailsOf(t, result)
		requireTextDetail(t, details, "stage", "enter")
		requireTextDetail(t, details, "reason", "enter_context_unconfirmed")
		requireTextDispatch(t, details, "dispatched", "dispatched", "not_attempted")
		tap, set, get, key := dev.counts()
		if tap != 1 || set != 1 || get != 0 || key != 0 {
			t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 1/1/0/0",
				tap, set, get, key)
		}
	})
}

// TestTextAlreadyFocusedStillRequiresExactSetValue proves the fast path does
// not accept an unreadable refreshed value just because the field was focused
// and the write was issued.
func TestTextAlreadyFocusedStillRequiresExactSetValue(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", true))
	dev.setSteps = []textSetStep{{result: device.TextResult{
		CandidateCount: 1, Value: nil, Issued: true, Receipt: device.DispatchTerminated,
	}}}
	falseValue := false
	result := h.actWith(t, "PtextFocusedNull", textFlowRequest(obsRef, ref, "hello", &falseValue))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("outcome = %q, want uncertain (%+v)", result.Outcome, result)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "stage", "verify")
	requireTextDetail(t, details, "reason", "readback_unavailable")
	requireTextDispatch(t, details, "not_attempted", "dispatched", "not_attempted")
	tap, set, get, key := dev.counts()
	if tap != 0 || set != 1 || get != 0 || key != 0 {
		t.Fatalf("primitive calls tap=%d set=%d get=%d key=%d, want 0/1/0/0",
			tap, set, get, key)
	}
}

// TestTextStaticRejectionCarriesUndispatchedDetails proves the pre-claim text
// ref rejection reports the target stage and all three primitives as not
// attempted without claiming the observation.
func TestTextStaticRejectionCarriesUndispatchedDetails(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, rec := newRevalHarness(t)
	dev := &textEvDevice{revalDevice: rec}
	h.engine.dev = dev
	h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		root := revalRoot(display.ID, 1, 2)
		button := revalButton(1, 0, 1, display.ID,
			device.Rect{Left: 100, Top: 100, Right: 900, Bottom: 200}, "OK", "", "ok_button")
		input := revalEditText(2, 0, 1, display.ID, bounds, "", "", "comment_box", false)
		return revalCapture(display, root, button, input), nil
	})
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button node not found")
	}
	falseValue := false
	result := h.actWith(t, "PtextStatic",
		textFlowRequest(start.Observation.ID, button.Ref, "hello", &falseValue))
	if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
		result.Error.Code != protocol.CodeTextTargetUnknown || result.Error.Details == nil {
		t.Fatalf("result = %+v, want text_target_uncertain/not_executed with details", result)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "stage", "target")
	requireTextDetail(t, details, "reason", "target_not_editable")
	requireTextDispatch(t, details, "not_attempted", "not_attempted", "not_attempted")
	if result.HTTPStatus != 422 {
		t.Fatalf("static text HTTP status hint = %d, want 422", result.HTTPStatus)
	}
	status, apiErr := h.engine.Status("agent", "PtextStatic")
	if apiErr != nil {
		t.Fatalf("status: %+v", apiErr)
	}
	if status.HTTPStatus != 422 {
		t.Fatalf("static text status(P) hint = %d, want 422", status.HTTPStatus)
	}
	if result.Recovery == nil || result.Recovery.Entry != protocol.RecoveryEntryRetry ||
		result.Recovery.Reference != protocol.RecoveryReferenceO ||
		result.Recovery.InputState != protocol.RecoveryInputEffective {
		t.Fatalf("static text recovery = %+v, want retry/O/effective", result.Recovery)
	}
	tap, set, get, key := dev.counts()
	if tap != 0 || set != 0 || get != 0 || key != 0 {
		t.Fatalf("static rejection dispatched input: tap=%d set=%d get=%d key=%d", tap, set, get, key)
	}
	requireReadyObservation(t, h, start.Observation.ID)
}

// TestTextFinishedUncertainPlansObserveForFirstStatusAndDuplicate proves the
// same semantic projection is used by the first result, status(P) and a
// duplicate P, and that none of them re-reads the device.
func TestTextFinishedUncertainPlansObserveForFirstStatusAndDuplicate(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true))
	dev.setSteps = []textSetStep{{result: device.TextResult{
		CandidateCount: 1, Value: strPtr("other"), Issued: true, Receipt: device.DispatchTerminated,
	}}}
	result := h.actWith(t, "PtextRecovery", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("outcome = %q, want uncertain", result.Outcome)
	}
	requireObserveRecovery(t, result.Recovery)
	tap, set, get, key := dev.counts()

	status, apiErr := h.engine.Status("agent", "PtextRecovery")
	if apiErr != nil {
		t.Fatalf("status: %+v", apiErr)
	}
	requireObserveRecovery(t, status.Recovery)
	if status.Error == nil || status.Error.Details == nil {
		t.Fatalf("status lost error details: %+v", status.Error)
	}

	duplicate := h.actWith(t, "PtextRecovery", textFlowRequest(obsRef, ref, "hello", nil))
	requireObserveRecovery(t, duplicate.Recovery)
	if duplicate.Error == nil || duplicate.Error.Details == nil {
		t.Fatalf("duplicate lost error details: %+v", duplicate.Error)
	}
	tap2, set2, get2, key2 := dev.counts()
	if tap2 != tap || set2 != set || get2 != get || key2 != key {
		t.Fatalf("status/duplicate dispatched input: before=%d/%d/%d/%d after=%d/%d/%d/%d",
			tap, set, get, key, tap2, set2, get2, key2)
	}
}

func requireObserveRecovery(t *testing.T, recovery *protocol.Recovery) {
	t.Helper()
	if recovery == nil || recovery.Entry != protocol.RecoveryEntryObserve ||
		recovery.Reference != protocol.RecoveryReferenceP ||
		recovery.InputState != protocol.RecoveryInputRetired || recovery.Cause != "" {
		t.Fatalf("recovery = %+v, want observe/P/retired without cause", recovery)
	}
}

// TestTextFinishedUncertainLiveOverrides proves the read-only projection
// applies current session/durability/device blockers without changing the
// saved semantic recovery.
func TestTextFinishedUncertainLiveOverrides(t *testing.T) {
	newFinished := func(t *testing.T) (*harness, *textEvDevice) {
		t.Helper()
		bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
		h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
		h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
			textFlowBuild(0, bounds, "", true))
		dev.setSteps = []textSetStep{{result: device.TextResult{
			CandidateCount: 1, Value: strPtr("other"), Issued: true, Receipt: device.DispatchTerminated,
		}}}
		result := h.actWith(t, "PtextLive", textFlowRequest(obsRef, ref, "hello", nil))
		if result.Outcome != OutcomeUncertain {
			t.Fatalf("outcome = %q, want uncertain", result.Outcome)
		}
		return h, dev
	}

	t.Run("revoked_session", func(t *testing.T) {
		h, _ := newFinished(t)
		if _, apiErr := h.engine.Stop(context.Background(), &protocol.ControlStopRequest{
			AppInstanceID: h.appInstance, StopSeq: 1, Reason: "user_stop",
		}); apiErr != nil {
			t.Fatalf("stop: %+v", apiErr)
		}
		status, apiErr := h.engine.Status("agent", "PtextLive")
		if apiErr != nil {
			t.Fatalf("status: %+v", apiErr)
		}
		if status.Recovery == nil || status.Recovery.Entry != protocol.RecoveryEntryEnd ||
			status.Recovery.Reference != protocol.RecoveryReferenceP ||
			status.Recovery.InputState != protocol.RecoveryInputRevoked {
			// A pending cleanup intentionally keeps status(P).
			if status.Recovery == nil || status.Recovery.Entry != protocol.RecoveryEntryStatus {
				t.Fatalf("revoked text recovery = %+v, want end or pending-cleanup status", status.Recovery)
			}
		}
		if status.BlockingCode != protocol.CodeStopped {
			t.Fatalf("blocking_code = %q, want control_revoked", status.BlockingCode)
		}
	})

	t.Run("ledger_unsafe_then_recovered", func(t *testing.T) {
		h, _ := newFinished(t)
		h.engine.mu.Lock()
		base := h.engine.operations["PtextLive"].Recovery
		h.engine.ledgerUnsafe = true
		h.engine.mu.Unlock()
		status, apiErr := h.engine.Status("agent", "PtextLive")
		if apiErr != nil {
			t.Fatalf("status: %+v", apiErr)
		}
		if status.Recovery == nil || status.Recovery.Entry != protocol.RecoveryEntryStatus ||
			status.BlockingCode != "ledger_unavailable" {
			t.Fatalf("ledger-unsafe recovery = %+v blocking=%q", status.Recovery, status.BlockingCode)
		}
		h.engine.mu.Lock()
		h.engine.ledgerUnsafe = false
		saved := h.engine.operations["PtextLive"].Recovery
		h.engine.mu.Unlock()
		if saved != base {
			t.Fatal("live ledger blocker polluted the saved base recovery")
		}
		status, apiErr = h.engine.Status("agent", "PtextLive")
		if apiErr != nil {
			t.Fatalf("status after recovery: %+v", apiErr)
		}
		requireObserveRecovery(t, status.Recovery)
		if status.BlockingCode != "" {
			t.Fatalf("blocking_code after recovery = %q", status.BlockingCode)
		}
	})

	t.Run("device_unavailable", func(t *testing.T) {
		h, _ := newFinished(t)
		h.engine.SetDeviceReady(false)
		status, apiErr := h.engine.Status("agent", "PtextLive")
		if apiErr != nil {
			t.Fatalf("status: %+v", apiErr)
		}
		if status.Recovery == nil || status.Recovery.Entry != protocol.RecoveryEntryStatus ||
			status.BlockingCode != protocol.CodeDeviceUnavailable {
			t.Fatalf("device recovery = %+v blocking=%q", status.Recovery, status.BlockingCode)
		}
		h.engine.SetDeviceReady(true)
		status, apiErr = h.engine.Status("agent", "PtextLive")
		if apiErr != nil {
			t.Fatalf("status after device: %+v", apiErr)
		}
		requireObserveRecovery(t, status.Recovery)
	})

	t.Run("busy_work", func(t *testing.T) {
		h, _ := newFinished(t)
		ctx, cancel := context.WithCancel(context.Background())
		h.engine.mu.Lock()
		h.engine.work = &workState{OperationID: "Pbusy", Agent: "agent", Kind: WorkObserve,
			Start: time.Now(), Deadline: time.Now().Add(time.Minute), Ctx: ctx, Cancel: cancel}
		h.engine.mu.Unlock()
		status, apiErr := h.engine.Status("agent", "PtextLive")
		if apiErr != nil {
			t.Fatalf("status: %+v", apiErr)
		}
		if status.Recovery == nil || status.Recovery.Entry != protocol.RecoveryEntryStatus ||
			status.BlockingCode != protocol.CodeDeviceBusy {
			t.Fatalf("busy recovery = %+v blocking=%q", status.Recovery, status.BlockingCode)
		}
		if status.BlockingOperationID != "Pbusy" {
			t.Fatalf("blocking_operation_id = %q, want Pbusy", status.BlockingOperationID)
		}
		h.engine.mu.Lock()
		h.engine.work = nil
		h.engine.mu.Unlock()
		cancel()
		status, apiErr = h.engine.Status("agent", "PtextLive")
		if apiErr != nil {
			t.Fatalf("status after busy: %+v", apiErr)
		}
		requireObserveRecovery(t, status.Recovery)
	})

	t.Run("control_reason_requires_user", func(t *testing.T) {
		h, _ := newFinished(t)
		h.engine.mu.Lock()
		h.engine.app.StopReady = false
		h.engine.mu.Unlock()
		status, apiErr := h.engine.Status("agent", "PtextLive")
		if apiErr != nil {
			t.Fatalf("status: %+v", apiErr)
		}
		if status.Recovery == nil || status.Recovery.Entry != protocol.RecoveryEntryUser ||
			status.Recovery.Reference != protocol.RecoveryReferenceNone ||
			status.BlockingCode != protocol.CodeStopUnavailable {
			t.Fatalf("user recovery = %+v blocking=%q", status.Recovery, status.BlockingCode)
		}
	})
}

// TestTextFreezeRecordsCurrentStageAndDeadlineReason proves a work deadline
// freezes the timely recorded stage and reports deadline_exceeded instead of a
// readback failure.
func TestTextFreezeRecordsCurrentStageAndDeadlineReason(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true))
	dev.setSteps = []textSetStep{{result: exactText("hello")}}
	dev.setHook = func() {
		h.engine.mu.Lock()
		if h.engine.work != nil {
			h.engine.work.Deadline = time.Now().Add(-time.Second)
		}
		h.engine.mu.Unlock()
		h.engine.housekeeping(context.Background())
	}
	result := h.actWith(t, "PtextFreeze", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("frozen outcome = %q, want uncertain (%+v)", result.Outcome, result)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeTextResultUnknown {
		t.Fatalf("frozen error = %+v, want text_result_uncertain", result.Error)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "stage", "write")
	requireTextDetail(t, details, "reason", "deadline_exceeded")
	requireTextDispatch(t, details, "dispatched", "unknown", "not_attempted")
}

func strPtr(value string) *string { return &value }

// TestTextFinishedEventIsBounded proves the completion event carries only the
// approved enums/counters and never the requested text or actual value.
func TestTextFinishedEventIsBounded(t *testing.T) {
	sink := &syncBuffer{}
	log := eventlog.New(sink, "instance-test")
	h := newHarnessWithLogger(t, true, log)
	h.engine.log = log
	rec := &revalDevice{fakeDevice: h.device}
	dev := &textEvDevice{revalDevice: rec}
	h.engine.dev = dev
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false))
	start := h.startSession()
	input := currentTree(t, h.engine).Root.Children[0]
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true))
	dev.setSteps = []textSetStep{{result: device.TextResult{
		CandidateCount: 1, Value: strPtr("other-secret"), Issued: true, Receipt: device.DispatchTerminated,
	}}}
	result := h.actWith(t, "PtextLog", textFlowRequest(start.Observation.ID, input.Ref, "top-secret", nil))
	if result.Outcome != OutcomeUncertain {
		t.Fatalf("outcome = %q, want uncertain", result.Outcome)
	}
	events := waitForEvent(t, sink, "engine.text.finished")
	for _, required := range []string{
		`operation_id="PtextLog"`, `outcome="uncertain"`, `stage="verify"`,
		`reason="readback_mismatch"`, `focus_dispatch="dispatched"`,
		`write_dispatch="dispatched"`, `enter_dispatch="not_attempted"`,
	} {
		if !strings.Contains(events, required) {
			t.Fatalf("text event lacks %s: %s", required, events)
		}
	}
	for _, forbidden := range []string{"top-secret", "other-secret"} {
		if strings.Contains(events, forbidden) {
			t.Fatalf("text event leaked content %q: %s", forbidden, events)
		}
	}
}
