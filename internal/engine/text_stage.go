package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Private focus states. Focus is an input primitive of the composite text
// flow but is deliberately absent from the public text_input object.
const (
	textFocusNotAttempted = "not_attempted"
	textFocusNotExecuted  = "not_executed"
	textFocusUncertain    = "uncertain"
	textFocusExecuted     = "executed"
)

// Per-primitive dispatch facts. They answer whether the primitive was called,
// independently of whether its content was confirmed. "unknown" is a closed
// window without a usable receipt, never a claim that the call succeeded.
const (
	textDispatchNotAttempted  = "not_attempted"
	textDispatchNotDispatched = "not_dispatched"
	textDispatchDispatched    = "dispatched"
	textDispatchUnknown       = "unknown"
)

// Logical stages of one text action. currentStage is recorded at the boundary
// that is about to run, so a freeze can report the last timely stage without
// parsing work.Phase. "submit" names the one bounded submission primitive,
// whichever physical form it takes (ENTER or a submit_ref tap).
const (
	textStageTarget = "target"
	textStageFocus  = "focus"
	textStageWrite  = "write"
	textStageVerify = "verify"
	textStageSubmit = "submit"
)

// Closed reason vocabulary of text error details. The agent may rely on these
// values instead of parsing the controlled message.
const (
	textReasonTargetNotEditable        = "target_not_editable"
	textReasonTargetProtected          = "target_protected"
	textReasonTargetUnconfirmed        = "target_unconfirmed"
	textReasonFocusUnconfirmed         = "focus_unconfirmed"
	textReasonFocusNotSent             = "focus_not_sent"
	textReasonNoUniqueTarget           = "no_unique_target"
	textReasonDispatchNotSent          = "dispatch_not_sent"
	textReasonReceiptUnknown           = "receipt_unknown"
	textReasonRPCResultInvalid         = "rpc_result_invalid"
	textReasonReadbackUnavailable      = "readback_unavailable"
	textReasonReadbackMismatch         = "readback_mismatch"
	textReasonSubmitTargetUnconfirmed  = "submit_target_unconfirmed"
	textReasonSubmitContextUnconfirmed = "submit_context_unconfirmed"
	textReasonSubmitDisabled           = "submit_disabled"
	textReasonSubmitScopeBlocked       = "submit_scope_blocked"
	textReasonSubmitNotSent            = "submit_not_sent"
	textReasonSubmitUnconfirmed        = "submit_unconfirmed"
	textReasonAuthorizationRevoked     = "authorization_revoked"
	textReasonDeadlineExceeded         = "deadline_exceeded"
)

// textStage is the bounded cumulative evidence of one claimed text action,
// including explicit auto_submit=false. It lives on operationState so replacing
// work.UI cannot erase an earlier primitive fact. It is the engine's original
// operation fact, not a second authority: it is never persisted as ordinary
// action history and never grants a dispatch.
type textStage struct {
	autoSubmit bool

	focus  string
	write  string
	submit string

	submitAttempted bool

	dispatchFocus  string
	dispatchWrite  string
	dispatchSubmit string

	currentStage  string
	failureReason string
	failureCount  int
	failureKnown  bool

	// Immutable admission binding. Recovery may only point at a live session
	// that still matches all of these facts.
	sessionOrigin     string
	sessionEpoch      int64
	appInstanceID     string
	modeGeneration    string
	displayID         int
	displayGeneration string
}

func newTextStage(session *sessionState, autoSubmit bool) *textStage {
	if session == nil {
		return nil
	}
	stage := &textStage{
		autoSubmit:        autoSubmit,
		focus:             textFocusNotAttempted,
		write:             protocol.TextInputNotExecuted,
		submit:            protocol.TextInputNotExecuted,
		dispatchFocus:     textDispatchNotAttempted,
		dispatchWrite:     textDispatchNotAttempted,
		dispatchSubmit:    textDispatchNotAttempted,
		currentStage:      textStageTarget,
		sessionOrigin:     session.Origin,
		sessionEpoch:      session.Epoch,
		appInstanceID:     session.AppInstanceID,
		modeGeneration:    session.ModeGeneration,
		displayID:         session.Display.ID,
		displayGeneration: session.Display.Generation,
	}
	return stage
}

// textOverallOutcome is the single cumulative-outcome derivation for one text
// action. Its order is the fact hierarchy:
//  1. a confirmed write with no required submission, or with a confirmed
//     submission, is executed;
//  2. a confirmed write whose auto submission is still certainly not executed
//     (never attempted or definitely not dispatched) is partial: the action is
//     confirmed incomplete and has no unknown physical side effect;
//  3. any primitive that may have reached Android keeps the action uncertain;
//  4. otherwise nothing was dispatched.
func textOverallOutcome(stage *textStage) string {
	if stage == nil {
		return OutcomeNotExecuted
	}
	if stage.write == protocol.TextInputExecuted &&
		(!stage.autoSubmit || stage.submit == protocol.TextInputExecuted) {
		return OutcomeExecuted
	}
	if stage.write == protocol.TextInputExecuted && stage.autoSubmit &&
		stage.submit == protocol.TextInputNotExecuted &&
		(stage.dispatchSubmit == textDispatchNotAttempted ||
			stage.dispatchSubmit == textDispatchNotDispatched) &&
		stage.dispatchFocus != textDispatchUnknown &&
		stage.dispatchWrite != textDispatchUnknown {
		// The partial fact is only honest while every non-submit primitive
		// already carries a usable receipt: a confirmed write with a certain
		// absence of submission and zero unknown physical effect.
		return OutcomePartial
	}
	mayHaveDispatched := func(state string) bool {
		return state == textDispatchUnknown || state == textDispatchDispatched
	}
	if mayHaveDispatched(stage.dispatchFocus) ||
		mayHaveDispatched(stage.dispatchWrite) ||
		mayHaveDispatched(stage.dispatchSubmit) {
		return OutcomeUncertain
	}
	return OutcomeNotExecuted
}

// initTextStageLocked publishes the initial target-stage snapshot in the same
// critical section that claims the observation. The caller holds e.mu.
func (e *Engine) initTextStageLocked(op *operationState, session *sessionState) {
	if op == nil || op.TextStage != nil {
		return
	}
	stage := newTextStage(session, op.TextStageAutoSubmit)
	if stage == nil {
		return
	}
	op.TextStage = stage
	e.publishTextInputLocked(op)
}

// publishTextInputLocked replaces the public immutable snapshot. Only the
// composite branch has a public text_input object. The caller holds e.mu.
func (e *Engine) publishTextInputLocked(op *operationState) {
	if op == nil || op.TextStage == nil || !op.TextStage.autoSubmit {
		return
	}
	op.TextInput = &protocol.TextInputView{
		Write: op.TextStage.write, Submit: op.TextStage.submit,
	}
}

// textPrimitiveField applies one primitive-state transition. The caller holds
// e.mu and has already checked ownership.
func textPrimitiveField(stage *textStage, primitive, state string) bool {
	if stage == nil {
		return false
	}
	switch primitive {
	case "focus":
		stage.focus = state
	case "write":
		stage.write = state
	case "submit":
		stage.submit = state
	default:
		return false
	}
	return true
}

func textDispatchField(stage *textStage, primitive, state string) bool {
	if stage == nil {
		return false
	}
	switch primitive {
	case "focus":
		stage.dispatchFocus = state
	case "write":
		stage.dispatchWrite = state
	case "submit":
		stage.dispatchSubmit = state
	default:
		return false
	}
	return true
}

// markTextPrimitiveLocked advances one primitive together with its dispatch
// fact while the corresponding dispatch authorization is being granted. It is
// called only inside beginUIDispatchStage's short lock. The caller holds e.mu.
func (e *Engine) markTextPrimitiveLocked(work *workState, primitive, state string) {
	if work == nil || primitive == "" {
		return
	}
	op := e.operations[work.OperationID]
	if op == nil || op.State != OperationRunning || op.TextStage == nil {
		return
	}
	if !textPrimitiveField(op.TextStage, primitive, state) {
		return
	}
	e.publishTextInputLocked(op)
}

// commitTextReceipt records one primitive's physical receipt together with
// its dispatch fact in one short lock. A timely terminated call is "dispatched"
// even when its semantic result is an error; a definite non-dispatch is
// "not_dispatched"; anything without a usable end receipt is "unknown". The
// write primitive's confirmation is deliberately not derived here. A STOP that
// already finished the operation wins: a late receipt never rewrites the frozen
// conservative facts.
func (e *Engine) commitTextReceipt(work *workState, op *operationState,
	primitive string, receipt device.DispatchReceipt, callErr error) bool {
	if op == nil || op.TextStage == nil || primitive == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	dispatch := textDispatchUnknown
	switch receipt {
	case device.DispatchTerminated:
		dispatch = textDispatchDispatched
	case device.DispatchNotDispatched:
		dispatch = textDispatchNotDispatched
	}
	textDispatchField(op.TextStage, primitive, dispatch)
	switch primitive {
	case "focus":
		switch {
		case receipt == device.DispatchNotDispatched:
			op.TextStage.focus = textFocusNotExecuted
		case receipt == device.DispatchTerminated && callErr == nil:
			op.TextStage.focus = textFocusExecuted
		default:
			op.TextStage.focus = textFocusUncertain
		}
	case "write":
		switch {
		case receipt == device.DispatchNotDispatched:
			op.TextStage.write = protocol.TextInputNotExecuted
		case receipt == device.DispatchTerminated:
			// The write stays uncertain until the refreshed value commits.
		default:
			op.TextStage.write = protocol.TextInputUncertain
		}
	case "submit":
		switch {
		case receipt == device.DispatchNotDispatched:
			op.TextStage.submit = protocol.TextInputNotExecuted
		case receipt == device.DispatchTerminated && callErr == nil:
			// confirmTextSubmit publishes executed together with KnownOutcome.
		default:
			op.TextStage.submit = protocol.TextInputUncertain
		}
	}
	e.publishTextInputLocked(op)
	return true
}

// setTextStage records the logical stage that is about to run. The update is
// conditional on the original work, session, display generation and immutable
// admission binding still being valid.
func (e *Engine) setTextStage(work *workState, op *operationState, stage string) bool {
	if op == nil || op.TextStage == nil || stage == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	op.TextStage.currentStage = stage
	return true
}

// noteTextWriteDispatched commits the timely verify boundary: the set call
// returned a complete response, the unique candidate was issued, and the
// actual refreshed value is about to be compared outside the lock.
func (e *Engine) noteTextWriteDispatched(work *workState, op *operationState) bool {
	if op == nil || op.TextStage == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	op.TextStage.dispatchWrite = textDispatchDispatched
	op.TextStage.currentStage = textStageVerify
	return true
}

// setTextWriteNotDispatched commits a complete set response that proves the
// fixed helper never called the write primitive (candidate count is not one).
func (e *Engine) setTextWriteNotDispatched(work *workState, op *operationState) bool {
	if op == nil || op.TextStage == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	op.TextStage.dispatchWrite = textDispatchNotDispatched
	op.TextStage.write = protocol.TextInputNotExecuted
	e.publishTextInputLocked(op)
	return true
}

// commitTextWrite publishes the confirmed write. The explicit-false branch also
// commits KnownOutcome=executed in the same lock, so STOP cannot split the two
// facts. The composite branch leaves the submission to its own independent
// gate; a later confirmed absence of submission derives OutcomePartial.
func (e *Engine) commitTextWrite(work *workState, op *operationState) bool {
	if op == nil || op.TextStage == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	op.TextStage.dispatchWrite = textDispatchDispatched
	op.TextStage.write = protocol.TextInputExecuted
	if !op.TextStage.autoSubmit {
		op.KnownOutcome = OutcomeExecuted
	} else {
		// The submission is the next boundary this action will run. Recording
		// it in the same lock as the confirmed write keeps a freeze between the
		// two from reporting the completed write stage as if the action had
		// never advanced past verify.
		op.TextStage.currentStage = textStageSubmit
	}
	e.publishTextInputLocked(op)
	return true
}

// beginTextSubmit records the one allowed submission attempt and checks the
// write stage and operation ownership before may_dispatch. It is one gate for
// both physical submission forms (ENTER and a submit_ref tap), so no call can
// obtain two submission primitives.
func (e *Engine) beginTextSubmit(work *workState, op *operationState) bool {
	if op == nil || op.TextStage == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		op.TextStage.submitAttempted ||
		op.TextStage.write != protocol.TextInputExecuted ||
		op.TextStage.submit != protocol.TextInputNotExecuted ||
		!op.TextStage.autoSubmit ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	op.TextStage.submitAttempted = true
	return true
}

// confirmTextSubmit publishes submit=executed and KnownOutcome=executed in one
// critical section. A freeze that wins first leaves the submission fact
// uncertain; a late receipt never rewrites it.
func (e *Engine) confirmTextSubmit(work *workState, op *operationState) bool {
	if op == nil || op.TextStage == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		op.TextStage.write != protocol.TextInputExecuted ||
		op.TextStage.submit != protocol.TextInputUncertain ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	op.TextStage.submit = protocol.TextInputExecuted
	e.publishTextInputLocked(op)
	op.KnownOutcome = OutcomeExecuted
	return true
}

// textStageAuthorizedLocked requires the immutable admission binding to still
// match the current session. A session display switch, App instance change or
// mode regeneration invalidates any pending text primitive rather than
// binding it to a different screen.
func (e *Engine) textStageAuthorizedLocked(work *workState) bool {
	if work == nil {
		return false
	}
	op := e.operations[work.OperationID]
	if op == nil || op.TextStage == nil {
		return true
	}
	return e.session != nil &&
		e.session.Origin == op.TextStage.sessionOrigin &&
		e.session.Epoch == op.TextStage.sessionEpoch &&
		e.session.AppInstanceID == op.TextStage.appInstanceID &&
		e.session.ModeGeneration == op.TextStage.modeGeneration &&
		e.session.Display.ID == op.TextStage.displayID &&
		e.session.Display.Generation == op.TextStage.displayGeneration
}

// textStageSnapshotLocked projects the bounded dispatch facts for one text
// error. The caller holds e.mu.
func textStageSnapshotLocked(op *operationState) (focus, write, submit string) {
	if op == nil || op.TextStage == nil {
		return textDispatchNotAttempted, textDispatchNotAttempted, textDispatchNotAttempted
	}
	return op.TextStage.dispatchFocus, op.TextStage.dispatchWrite, op.TextStage.dispatchSubmit
}

// textError builds one controlled text error together with the stage, reason,
// dispatch facts and optional candidate count. The dispatch snapshot and the
// last failure reason are read from the original operation fact, never from a
// message or work.Phase.
func (e *Engine) textError(op *operationState, status int, code, message, stage, reason, outcome string, count *int) *protocol.APIError {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.textErrorLocked(op, status, code, message, stage, reason, outcome, count)
}

// textErrorLocked is the same builder for a caller that already holds e.mu. It
// lets a terminal decision and its saved error share one critical section.
func (e *Engine) textErrorLocked(op *operationState, status int, code, message, stage, reason, outcome string, count *int) *protocol.APIError {
	focus, write, submit := textStageSnapshotLocked(op)
	if op != nil && op.State == OperationRunning && op.TextStage != nil {
		op.TextStage.failureReason = reason
		if count != nil {
			op.TextStage.failureCount = *count
			op.TextStage.failureKnown = true
		}
	}
	details := map[string]any{
		"stage":  stage,
		"reason": reason,
		"dispatch": map[string]any{
			"focus": focus, "write": write, "submit": submit,
		},
	}
	if count != nil {
		details["candidate_count"] = *count
	}
	return &protocol.APIError{
		Code: code, Message: message, HTTPStatus: status, Outcome: outcome, Details: details,
	}
}

// textCurrentStage reads the timely logical stage for error construction.
func (e *Engine) textCurrentStage(op *operationState) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if op == nil || op.TextStage == nil || op.TextStage.currentStage == "" {
		return textStageTarget
	}
	return op.TextStage.currentStage
}

// textFailureOutcome computes the conservative cumulative outcome.
func (e *Engine) textFailureOutcome(op *operationState) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if op == nil {
		return OutcomeNotExecuted
	}
	if op.KnownOutcome != "" {
		return op.KnownOutcome
	}
	return textOverallOutcome(op.TextStage)
}
