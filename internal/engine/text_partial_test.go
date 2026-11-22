package engine

import (
	"context"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// TestTextOverallOutcomePartialMatrix pins the four-value derivation and its
// strict ordering: executed first, partial only with a certain absence of
// submission and zero unknown physical effect, then uncertain, then nothing.
func TestTextOverallOutcomePartialMatrix(t *testing.T) {
	base := func() *textStage {
		return &textStage{
			autoSubmit:     true,
			focus:          textFocusNotAttempted,
			write:          protocol.TextInputNotExecuted,
			submit:         protocol.TextInputNotExecuted,
			dispatchFocus:  textDispatchNotAttempted,
			dispatchWrite:  textDispatchNotAttempted,
			dispatchSubmit: textDispatchNotAttempted,
		}
	}
	cases := []struct {
		name   string
		mutate func(*textStage)
		want   string
	}{
		{
			name: "write_confirmed_submit_never_attempted",
			mutate: func(stage *textStage) {
				stage.write = protocol.TextInputExecuted
				stage.dispatchWrite = textDispatchDispatched
			},
			want: OutcomePartial,
		},
		{
			name: "write_confirmed_submit_not_dispatched",
			mutate: func(stage *textStage) {
				stage.write = protocol.TextInputExecuted
				stage.dispatchWrite = textDispatchDispatched
				stage.dispatchSubmit = textDispatchNotDispatched
			},
			want: OutcomePartial,
		},
		{
			name: "write_confirmed_submit_dispatched_but_unconfirmed",
			mutate: func(stage *textStage) {
				stage.write = protocol.TextInputExecuted
				stage.dispatchWrite = textDispatchDispatched
				stage.dispatchSubmit = textDispatchDispatched
			},
			want: OutcomeUncertain,
		},
		{
			name: "write_confirmed_submit_unknown",
			mutate: func(stage *textStage) {
				stage.write = protocol.TextInputExecuted
				stage.dispatchWrite = textDispatchDispatched
				stage.submit = protocol.TextInputUncertain
				stage.dispatchSubmit = textDispatchUnknown
			},
			want: OutcomeUncertain,
		},
		{
			name: "write_confirmed_submit_executed",
			mutate: func(stage *textStage) {
				stage.write = protocol.TextInputExecuted
				stage.submit = protocol.TextInputExecuted
				stage.dispatchWrite = textDispatchDispatched
				stage.dispatchSubmit = textDispatchDispatched
			},
			want: OutcomeExecuted,
		},
		{
			name: "write_confirmed_auto_submit_false",
			mutate: func(stage *textStage) {
				stage.autoSubmit = false
				stage.write = protocol.TextInputExecuted
				stage.dispatchWrite = textDispatchDispatched
			},
			want: OutcomeExecuted,
		},
		{
			name: "write_not_confirmed_never_partial",
			mutate: func(stage *textStage) {
				stage.dispatchWrite = textDispatchDispatched
			},
			want: OutcomeUncertain,
		},
		{
			name: "nothing_dispatched",
			mutate: func(stage *textStage) {
			},
			want: OutcomeNotExecuted,
		},
		{
			name: "focus_dispatched_alone_is_uncertain",
			mutate: func(stage *textStage) {
				stage.dispatchFocus = textDispatchDispatched
			},
			want: OutcomeUncertain,
		},
		{
			name: "unknown_focus_never_partial",
			mutate: func(stage *textStage) {
				stage.write = protocol.TextInputExecuted
				stage.dispatchWrite = textDispatchDispatched
				stage.dispatchFocus = textDispatchUnknown
			},
			want: OutcomeUncertain,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stage := base()
			tc.mutate(stage)
			if got := textOverallOutcome(stage); got != tc.want {
				t.Fatalf("textOverallOutcome = %q, want %q (stage=%+v)", got, tc.want, stage)
			}
		})
	}
}

// newClaimedTextOperation builds one running text operation bound to the
// harness session exactly as a claim would have bound it.
func newClaimedTextOperation(t *testing.T, h *harness, id string, autoSubmit bool) (*operationState, *sessionState) {
	t.Helper()
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	session := *h.engine.session
	op := &operationState{
		ID: id, Agent: "agent", Kind: "act", State: OperationRunning,
		Phase: "dispatch", ActAction: "text", Claimed: true,
		SessionOrigin: session.Origin,
	}
	op.TextStage = newTextStage(&session, autoSubmit)
	h.engine.operations[id] = op
	return op, &session
}

// TestFreezeTextPartialSingleSourceFacts proves freezeTextLocked reuses the
// op.Outcome already derived from the cumulative facts and classifies the
// terminal by the live session binding: revoked is control_revoked, only a live
// same-generation session with its own work deadline is the partial timeout,
// and a wrong display generation is never mistaken for an ordinary timeout.
func TestFreezeTextPartialSingleSourceFacts(t *testing.T) {
	t.Run("live_deadline_is_partial_submit_not_executed", func(t *testing.T) {
		h := newHarness(t)
		h.startSession()
		op, session := newClaimedTextOperation(t, h, "PfreezeDeadline", true)
		op.TextStage.write = protocol.TextInputExecuted
		op.TextStage.submit = protocol.TextInputNotExecuted
		op.TextStage.dispatchWrite = textDispatchDispatched
		// Deliberately leave the recorded stage at target: a partial freeze must
		// report the submission boundary from the confirmed facts, never from a
		// stale stage field.
		op.TextStage.currentStage = textStageTarget
		work := freezeTextWork(op.ID, session, time.Now().Add(-time.Second), context.Background())

		h.engine.mu.Lock()
		h.engine.freezeWorkLocked(work)
		h.engine.mu.Unlock()

		if op.Outcome != OutcomePartial {
			t.Fatalf("frozen outcome = %q, want partial", op.Outcome)
		}
		if op.Error == nil || op.Error.Code != protocol.CodeTextSubmitNotExecuted {
			t.Fatalf("frozen error = %+v, want text_submit_not_executed", op.Error)
		}
		requireFrozenSubmitDetails(t, op.Error, "submit", "deadline_exceeded", "not_attempted")
	})

	t.Run("revoked_session_is_control_revoked", func(t *testing.T) {
		h := newHarness(t)
		h.startSession()
		op, session := newClaimedTextOperation(t, h, "PfreezeRevoked", true)
		op.TextStage.write = protocol.TextInputExecuted
		op.TextStage.dispatchWrite = textDispatchDispatched
		op.TextStage.currentStage = textStageSubmit
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		work := freezeTextWork(op.ID, session, time.Now().Add(time.Minute), ctx)

		h.engine.mu.Lock()
		h.engine.freezeWorkLocked(work)
		h.engine.mu.Unlock()

		if op.Outcome != OutcomePartial {
			t.Fatalf("revoked frozen outcome = %q, want partial", op.Outcome)
		}
		if op.Error == nil || op.Error.Code != protocol.CodeStopped {
			t.Fatalf("revoked frozen error = %+v, want control_revoked", op.Error)
		}
		requireFrozenSubmitDetails(t, op.Error, "submit", "authorization_revoked", "not_attempted")
	})

	t.Run("wrong_display_generation_is_not_a_timeout", func(t *testing.T) {
		h := newHarness(t)
		h.startSession()
		op, session := newClaimedTextOperation(t, h, "PfreezeGeneration", true)
		op.TextStage.write = protocol.TextInputExecuted
		op.TextStage.dispatchWrite = textDispatchDispatched
		op.TextStage.currentStage = textStageSubmit
		work := freezeTextWork(op.ID, session, time.Now().Add(-time.Second), context.Background())

		h.engine.mu.Lock()
		h.engine.session.Display.Generation = "regenerated"
		h.engine.freezeWorkLocked(work)
		h.engine.mu.Unlock()

		if op.Outcome != OutcomePartial {
			t.Fatalf("generation-mismatch frozen outcome = %q, want partial", op.Outcome)
		}
		if op.Error == nil || op.Error.Code != protocol.CodeStopped {
			t.Fatalf("generation-mismatch frozen error = %+v, want control_revoked", op.Error)
		}
		requireFrozenSubmitDetails(t, op.Error, "submit", "authorization_revoked", "not_attempted")
	})

	t.Run("unconfirmed_write_stays_uncertain", func(t *testing.T) {
		h := newHarness(t)
		h.startSession()
		op, session := newClaimedTextOperation(t, h, "PfreezeUncertain", true)
		op.TextStage.write = protocol.TextInputUncertain
		op.TextStage.dispatchWrite = textDispatchUnknown
		op.TextStage.currentStage = textStageWrite
		work := freezeTextWork(op.ID, session, time.Now().Add(-time.Second), context.Background())

		h.engine.mu.Lock()
		h.engine.freezeWorkLocked(work)
		h.engine.mu.Unlock()

		if op.Outcome != OutcomeUncertain {
			t.Fatalf("unconfirmed write frozen outcome = %q, want uncertain", op.Outcome)
		}
		if op.Error == nil || op.Error.Code != protocol.CodeTextResultUnknown {
			t.Fatalf("unconfirmed write frozen error = %+v, want text_result_uncertain", op.Error)
		}
	})
}

func freezeTextWork(operationID string, session *sessionState, deadline time.Time,
	ctx context.Context) *workState {
	return &workState{
		OperationID: operationID, Agent: "agent", Kind: WorkAct,
		SessionEpoch: session.Epoch, Start: time.Now(), Deadline: deadline, Ctx: ctx,
	}
}

func requireFrozenSubmitDetails(t *testing.T, public *protocol.Error, stage, reason, submit string) {
	t.Helper()
	if public.Details == nil {
		t.Fatalf("frozen error has no details: %+v", public)
	}
	if public.Details["stage"] != stage || public.Details["reason"] != reason {
		t.Fatalf("frozen details = %+v, want stage=%s reason=%s", public.Details, stage, reason)
	}
	dispatch, ok := public.Details["dispatch"].(map[string]any)
	if !ok || dispatch["submit"] != submit {
		t.Fatalf("frozen dispatch = %#v, want submit=%s", public.Details["dispatch"], submit)
	}
}

// TestPlanRecoveryPartialKeepsObserveBase proves the persisted semantic base of
// a claimed partial text operation is one new observation, never nil, so both
// the first response and every replay pass through the live overlay.
func TestPlanRecoveryPartialKeepsObserveBase(t *testing.T) {
	for _, code := range []string{protocol.CodeTextSubmitNotExecuted, protocol.CodeStopped} {
		t.Run(code, func(t *testing.T) {
			recovery := planRecoveryFor(recoveryInput{
				Tool: "act", Reference: protocol.RecoveryReferenceO, Code: code,
				Phase: "partial", Outcome: OutcomePartial, Claimed: true, ActAction: "text",
			})
			if recovery == nil || recovery.Entry != protocol.RecoveryEntryObserve ||
				recovery.Reference != protocol.RecoveryReferenceP ||
				recovery.InputState != protocol.RecoveryInputRetired || recovery.Cause != "" {
				t.Fatalf("partial recovery = %+v, want observe/P/retired without cause", recovery)
			}
		})
	}
}

// TestApplyLiveTextRecoveryPartialOmissionRules proves the result-level
// projection omits recovery only for a real current ready observation, removes
// an identity that lost authority in the same short lock, keeps a failed
// read-only placeholder, and lets live blockers override the entry.
func TestApplyLiveTextRecoveryPartialOmissionRules(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	e := h.engine
	base := newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, "")
	newPartialOp := func(t *testing.T) (*operationState, *observationState) {
		t.Helper()
		op, _ := newClaimedTextOperation(t, h, "PlivePartial", true)
		op.State = OperationFinished
		op.Outcome = OutcomePartial
		op.TextStage.write = protocol.TextInputExecuted
		op.TextStage.submit = protocol.TextInputNotExecuted
		op.TextStage.dispatchWrite = textDispatchDispatched
		op.TextStage.currentStage = textStageSubmit
		e.mu.Lock()
		current := e.current
		e.mu.Unlock()
		return op, current
	}

	t.Run("valid_current_ready_observation_omits_recovery", func(t *testing.T) {
		op, current := newPartialOp(t)
		result := &protocol.Result{Recovery: base,
			Observation: &protocol.Observation{ID: current.ID, Status: "complete"}}
		e.mu.Lock()
		e.applyLiveTextRecoveryLocked(op, result, time.Now())
		e.mu.Unlock()
		if result.Recovery != nil {
			t.Fatalf("recovery = %+v, want nil for a valid attached ready O", result.Recovery)
		}
		if result.Observation == nil || result.Observation.ID != current.ID {
			t.Fatalf("valid observation was dropped: %+v", result.Observation)
		}
	})

	t.Run("replayed_view_keeps_observe_base", func(t *testing.T) {
		op, _ := newPartialOp(t)
		result := &protocol.Result{Recovery: base}
		e.mu.Lock()
		e.applyLiveTextRecoveryLocked(op, result, time.Now())
		e.mu.Unlock()
		if result.Recovery == nil || *result.Recovery != *base {
			t.Fatalf("replayed recovery = %+v, want persisted observe base", result.Recovery)
		}
	})

	t.Run("failed_placeholder_is_kept_but_never_omits_recovery", func(t *testing.T) {
		op, _ := newPartialOp(t)
		result := &protocol.Result{Recovery: base,
			Observation: &protocol.Observation{Status: "failed", Missing: []string{"tree"}}}
		e.mu.Lock()
		e.applyLiveTextRecoveryLocked(op, result, time.Now())
		e.mu.Unlock()
		if result.Observation == nil || result.Observation.Status != "failed" {
			t.Fatalf("failed placeholder was dropped: %+v", result.Observation)
		}
		if result.Recovery == nil || *result.Recovery != *base {
			t.Fatalf("failed placeholder recovery = %+v, want persisted observe base", result.Recovery)
		}
	})

	t.Run("stale_identity_is_removed_in_the_same_lock", func(t *testing.T) {
		op, _ := newPartialOp(t)
		result := &protocol.Result{Recovery: base,
			Observation: &protocol.Observation{ID: "O-stale", Status: "complete"}}
		e.mu.Lock()
		e.applyLiveTextRecoveryLocked(op, result, time.Now())
		e.mu.Unlock()
		if result.Observation != nil {
			t.Fatalf("stale observation was kept: %+v", result.Observation)
		}
		if result.Recovery == nil || *result.Recovery != *base {
			t.Fatalf("stale observation recovery = %+v, want persisted observe base", result.Recovery)
		}
	})

	t.Run("ledger_blocker_overrides_entry", func(t *testing.T) {
		op, current := newPartialOp(t)
		result := &protocol.Result{Recovery: base,
			Observation: &protocol.Observation{ID: current.ID, Status: "complete"}}
		e.mu.Lock()
		e.ledgerUnsafe = true
		e.applyLiveTextRecoveryLocked(op, result, time.Now())
		e.mu.Unlock()
		e.mu.Lock()
		e.ledgerUnsafe = false
		e.mu.Unlock()
		if result.Recovery == nil || result.Recovery.Entry != protocol.RecoveryEntryStatus {
			t.Fatalf("ledger-unsafe recovery = %+v, want status", result.Recovery)
		}
		if result.BlockingCode != "ledger_unavailable" {
			t.Fatalf("ledger-unsafe blocking_code = %q", result.BlockingCode)
		}
	})
}

// TestPublishTextPartialCaptureRejectsOtherDisplayInstance proves a reused
// after-write frame from another display instance or generation is never
// republished as this session's observation: the guard runs before any
// projection, so no tree work happens and the caller falls back to a standard
// capture.
func TestPublishTextPartialCaptureRejectsOtherDisplayInstance(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	e := h.engine
	e.mu.Lock()
	session := *e.session
	work := &workState{
		OperationID: "PpartialStale", Agent: "agent", Kind: WorkAct,
		SessionEpoch: session.Epoch, Start: time.Now(),
		Deadline: time.Now().Add(time.Minute), Ctx: context.Background(),
	}
	e.work = work
	e.mu.Unlock()
	t.Cleanup(func() {
		e.mu.Lock()
		if e.work == work {
			e.work = nil
		}
		e.mu.Unlock()
	})

	stale := device.Capture{
		Display:    device.Display{ID: session.Display.ID, Generation: "other-generation", Width: session.Display.Width, Height: session.Display.Height},
		Foreground: device.Foreground{State: device.ForegroundPresent, Package: "com.example"},
		Nodes:      []device.RawNode{{Index: 0, Parent: -1, DisplayID: session.Display.ID, Visible: true}},
	}
	public, stopped := e.publishTextPartialCapture(work, &session, stale)
	if public != nil || stopped != nil {
		t.Fatalf("stale-display frame was published or stopped: public=%+v stopped=%+v", public, stopped)
	}
}

// TestActTextPartialObservationFailureKeepsObserveRecovery proves a partial
// delivery whose post observation cannot be read still reports the confirmed
// write, attaches only the failed read-only placeholder and keeps the persisted
// observe(P, retired) recovery instead of omitting it.
func TestActTextPartialObservationFailureKeepsObserveRecovery(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true))
	dev.setSteps = []textSetStep{{result: exactText("hello")}}
	changed := "different"
	dev.getSteps = []textGetStep{{result: device.TextResult{CandidateCount: 1, Value: &changed}}}
	dev.setHook = func() {
		h.device.mu.Lock()
		h.device.captureErrs = true
		h.device.mu.Unlock()
	}
	h.sync(nil, nil)
	result := h.actWith(t, "PtextPartialNoObservation", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomePartial || result.Error == nil ||
		result.Error.Code != protocol.CodeTextSubmitNotExecuted {
		t.Fatalf("result = %+v, want partial/text_submit_not_executed", result)
	}
	if result.Observation == nil || result.Observation.Status != "failed" ||
		result.Observation.ID != "" {
		t.Fatalf("observation = %+v, want a failed read-only placeholder without id", result.Observation)
	}
	if result.TextInput == nil || result.TextInput.Write != protocol.TextInputExecuted ||
		result.TextInput.Submit != protocol.TextInputNotExecuted {
		t.Fatalf("text_input = %+v, want write executed / submit not_executed", result.TextInput)
	}
	requireObserveRecovery(t, result.Recovery)
}

// TestActTextAutoSubmitFalseIgnoresSubmitRef proves the tolerant rule: with an
// effective auto_submit=false a syntactically valid submit_ref is ignored, so
// only the write happens and the public text_input object stays absent.
func TestActTextAutoSubmitFalseIgnoresSubmitRef(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextFlowHarness(t, bounds)
	h.device.scriptCaptures(
		textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true),
		textFlowBuild(0, bounds, "draft", true),
	)
	request := textFlowRequest(obsRef, ref, "draft", nil)
	request.Operation.AutoSubmit, request.Operation.HasAutoSubmit = false, true
	request.Operation.SubmitRef, request.Operation.HasSubmitRef = "n99", true
	result := h.actWith(t, "PtextIgnoreSubmitRef", request)
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("ignored submit_ref result = %+v, want executed without error", result)
	}
	if result.TextInput != nil {
		t.Fatalf("auto_submit=false published text_input: %+v", result.TextInput)
	}
	if dev.keyCalls != 0 {
		t.Fatalf("auto_submit=false sent %d keys, want 0", dev.keyCalls)
	}
	if len(dev.recordedTaps()) != 1 {
		t.Fatalf("auto_submit=false taps = %+v, want only the focus tap", dev.recordedTaps())
	}
}

// TestPartialTextAuthorityClassification pins the one classification used by the
// partial terminal: a live same-generation session whose own work budget expired
// is a deadline, never a revocation; a cancelled context, a wrong work position
// or an ended session is a revocation.
func TestPartialTextAuthorityClassification(t *testing.T) {
	classify := func(t *testing.T, prepare func(*Engine, *sessionState, *workState)) (bool, bool) {
		t.Helper()
		h := newHarness(t)
		h.startSession()
		e := h.engine
		_, session := newClaimedTextOperation(t, h, "Pauthority", true)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		work := &workState{
			OperationID: "Pauthority", Agent: "agent", Kind: WorkAct,
			SessionEpoch: session.Epoch, Start: time.Now(),
			Deadline: time.Now().Add(time.Minute), Ctx: ctx,
		}
		e.mu.Lock()
		e.work = work
		prepare(e, session, work)
		revoked, deadline := e.partialTextAuthorityLocked(work, time.Now())
		e.work = nil
		e.mu.Unlock()
		return revoked, deadline
	}

	t.Run("live_session_deadline_is_timeout", func(t *testing.T) {
		revoked, deadline := classify(t, func(_ *Engine, _ *sessionState, work *workState) {
			work.Deadline = time.Now().Add(-time.Second)
		})
		if revoked || !deadline {
			t.Fatalf("classification revoked=%t deadline=%t, want deadline only", revoked, deadline)
		}
	})

	t.Run("expired_context_deadline_is_timeout", func(t *testing.T) {
		revoked, deadline := classify(t, func(_ *Engine, _ *sessionState, work *workState) {
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			t.Cleanup(cancel)
			work.Ctx = ctx
		})
		if revoked || !deadline {
			t.Fatalf("classification revoked=%t deadline=%t, want deadline only", revoked, deadline)
		}
	})

	t.Run("cancelled_context_is_revocation", func(t *testing.T) {
		revoked, deadline := classify(t, func(_ *Engine, _ *sessionState, work *workState) {
			ctx, cancelFn := context.WithCancel(work.Ctx)
			cancelFn()
			work.Ctx = ctx
		})
		if !revoked || deadline {
			t.Fatalf("classification revoked=%t deadline=%t, want revocation only", revoked, deadline)
		}
	})

	t.Run("ended_session_is_revocation", func(t *testing.T) {
		revoked, deadline := classify(t, func(e *Engine, _ *sessionState, _ *workState) {
			e.session.State = SessionEnded
		})
		if !revoked || deadline {
			t.Fatalf("classification revoked=%t deadline=%t, want revocation only", revoked, deadline)
		}
	})

	t.Run("display_generation_change_is_revocation", func(t *testing.T) {
		revoked, deadline := classify(t, func(e *Engine, _ *sessionState, work *workState) {
			e.session.Display.Generation = "regenerated"
			work.Deadline = time.Now().Add(-time.Second)
		})
		if !revoked || deadline {
			t.Fatalf("classification revoked=%t deadline=%t, want revocation only", revoked, deadline)
		}
	})

	t.Run("lost_work_position_is_revocation", func(t *testing.T) {
		revoked, deadline := classify(t, func(e *Engine, _ *sessionState, _ *workState) {
			e.work = nil
		})
		if !revoked || deadline {
			t.Fatalf("classification revoked=%t deadline=%t, want revocation only", revoked, deadline)
		}
	})
}

// TestActTextPartialLiveDeadlineReportsSubmitNotExecuted proves a confirmed
// write whose submission never left the engine while the live session only ran
// out of its own work budget is the bounded partial timeout: 409
// text_submit_not_executed, no published observation, and the persisted
// observe(P, retired) recovery base. It is never mistaken for control_revoked.
func TestActTextPartialLiveDeadlineReportsSubmitNotExecuted(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, obsRef, ref := newTextEvidenceHarness(t, bounds)
	h.device.scriptCaptures(textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true))
	dev.setSteps = []textSetStep{{result: exactText("hello")}}
	changed := "different"
	dev.getSteps = []textGetStep{{result: device.TextResult{CandidateCount: 1, Value: &changed}}}
	dev.setHook = func() {
		h.engine.mu.Lock()
		if h.engine.work != nil {
			h.engine.work.Deadline = time.Now().Add(-time.Second)
		}
		h.engine.mu.Unlock()
	}
	result := h.actWith(t, "PtextPartialDeadline", textFlowRequest(obsRef, ref, "hello", nil))
	if result.Outcome != OutcomePartial || result.Error == nil ||
		result.Error.Code != protocol.CodeTextSubmitNotExecuted {
		t.Fatalf("result = %+v, want partial/text_submit_not_executed", result)
	}
	details := textDetailsOf(t, result)
	requireTextDetail(t, details, "stage", "submit")
	requireTextDetail(t, details, "reason", "deadline_exceeded")
	if result.Observation != nil {
		t.Fatalf("work-deadline partial published an observation: %+v", result.Observation)
	}
	requireObserveRecovery(t, result.Recovery)
}
