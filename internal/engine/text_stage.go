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
// parsing work.Phase.
const (
	textStageTarget = "target"
	textStageFocus  = "focus"
	textStageWrite  = "write"
	textStageVerify = "verify"
	textStageEnter  = "enter"
)

// Closed reason vocabulary of text error details. The agent may rely on these
// values instead of parsing the controlled message.
const (
	textReasonTargetNotEditable       = "target_not_editable"
	textReasonTargetProtected         = "target_protected"
	textReasonTargetUnconfirmed       = "target_unconfirmed"
	textReasonFocusUnconfirmed        = "focus_unconfirmed"
	textReasonFocusNotSent            = "focus_not_sent"
	textReasonNoUniqueTarget          = "no_unique_target"
	textReasonDispatchNotSent         = "dispatch_not_sent"
	textReasonReceiptUnknown          = "receipt_unknown"
	textReasonRPCResultInvalid        = "rpc_result_invalid"
	textReasonReadbackUnavailable     = "readback_unavailable"
	textReasonReadbackMismatch        = "readback_mismatch"
	textReasonEnterTargetUnconfirmed  = "enter_target_unconfirmed"
	textReasonEnterContextUnconfirmed = "enter_context_unconfirmed"
	textReasonEnterNotSent            = "enter_not_sent"
	textReasonEnterUnconfirmed        = "enter_unconfirmed"
	textReasonAuthorizationRevoked    = "authorization_revoked"
	textReasonDeadlineExceeded        = "deadline_exceeded"
)

// textStage is the bounded cumulative evidence of one claimed text action,
// including explicit press_enter=false. It lives on operationState so replacing
// work.UI cannot erase an earlier primitive fact. It is the engine's original
// operation fact, not a second authority: it is never persisted as ordinary
// action history and never grants a dispatch.
type textStage struct {
	pressEnter bool

	focus string
	write string
	enter string

	enterAttempted bool

	dispatchFocus string
	dispatchWrite string
	dispatchEnter string

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

func newTextStage(session *sessionState, pressEnter bool) *textStage {
	if session == nil {
		return nil
	}
	stage := &textStage{
		pressEnter:        pressEnter,
		focus:             textFocusNotAttempted,
		write:             protocol.TextInputNotExecuted,
		enter:             protocol.TextInputNotExecuted,
		dispatchFocus:     textDispatchNotAttempted,
		dispatchWrite:     textDispatchNotAttempted,
		dispatchEnter:     textDispatchNotAttempted,
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

// textOverallOutcome derives the conservative action outcome from cumulative
// primitive facts. Executed requires the write and, for the composite branch,
// both keys confirmed. Not_executed requires that no primitive ever obtained a
// dispatch permission; a dispatched focus alone keeps the whole action
// uncertain.
func textOverallOutcome(stage *textStage) string {
	if stage == nil {
		return OutcomeNotExecuted
	}
	if stage.write == protocol.TextInputExecuted &&
		(!stage.pressEnter || stage.enter == protocol.TextInputExecuted) {
		return OutcomeExecuted
	}
	mayHaveDispatched := func(state string) bool {
		return state == textDispatchUnknown || state == textDispatchDispatched
	}
	if mayHaveDispatched(stage.dispatchFocus) ||
		mayHaveDispatched(stage.dispatchWrite) ||
		mayHaveDispatched(stage.dispatchEnter) {
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
	stage := newTextStage(session, op.TextStagePressEnter)
	if stage == nil {
		return
	}
	op.TextStage = stage
	e.publishTextInputLocked(op)
}

// publishTextInputLocked replaces the public immutable snapshot. Only the
// composite branch has a public text_input object. The caller holds e.mu.
func (e *Engine) publishTextInputLocked(op *operationState) {
	if op == nil || op.TextStage == nil || !op.TextStage.pressEnter {
		return
	}
	op.TextInput = &protocol.TextInputView{
		Write: op.TextStage.write, Enter: op.TextStage.enter,
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
	case "enter":
		stage.enter = state
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
	case "enter":
		stage.dispatchEnter = state
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
// write primitive's confirmation is deliberately not derived here.
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
	case "enter":
		switch {
		case receipt == device.DispatchNotDispatched:
			op.TextStage.enter = protocol.TextInputNotExecuted
		case receipt == device.DispatchTerminated && callErr == nil:
			// confirmTextEnter publishes executed together with KnownOutcome.
		default:
			op.TextStage.enter = protocol.TextInputUncertain
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
// facts. The composite branch leaves ENTER to its own independent gate.
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
	if !op.TextStage.pressEnter {
		op.KnownOutcome = OutcomeExecuted
	}
	e.publishTextInputLocked(op)
	return true
}

// beginTextEnter records the one allowed ENTER attempt and checks the write
// stage and operation ownership before may_dispatch.
func (e *Engine) beginTextEnter(work *workState, op *operationState) bool {
	if op == nil || op.TextStage == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		op.TextStage.enterAttempted ||
		op.TextStage.write != protocol.TextInputExecuted ||
		op.TextStage.enter != protocol.TextInputNotExecuted ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	op.TextStage.enterAttempted = true
	return true
}

// confirmTextEnter publishes enter=executed and KnownOutcome=executed in one
// critical section. A freeze that wins first leaves the enter fact uncertain.
func (e *Engine) confirmTextEnter(work *workState, op *operationState) bool {
	if op == nil || op.TextStage == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		op.TextStage.write != protocol.TextInputExecuted ||
		op.TextStage.enter != protocol.TextInputUncertain ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	op.TextStage.enter = protocol.TextInputExecuted
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
func textStageSnapshotLocked(op *operationState) (focus, write, enter string) {
	if op == nil || op.TextStage == nil {
		return textDispatchNotAttempted, textDispatchNotAttempted, textDispatchNotAttempted
	}
	return op.TextStage.dispatchFocus, op.TextStage.dispatchWrite, op.TextStage.dispatchEnter
}

// textError builds one controlled text error together with the stage, reason,
// dispatch facts and optional candidate count. The dispatch snapshot and the
// last failure reason are read from the original operation fact, never from a
// message or work.Phase.
func (e *Engine) textError(op *operationState, status int, code, message, stage, reason, outcome string, count *int) *protocol.APIError {
	e.mu.Lock()
	focus, write, enter := textStageSnapshotLocked(op)
	if op != nil && op.State == OperationRunning && op.TextStage != nil {
		op.TextStage.failureReason = reason
		if count != nil {
			op.TextStage.failureCount = *count
			op.TextStage.failureKnown = true
		}
	}
	e.mu.Unlock()
	details := map[string]any{
		"stage":  stage,
		"reason": reason,
		"dispatch": map[string]any{
			"focus": focus, "write": write, "enter": enter,
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
