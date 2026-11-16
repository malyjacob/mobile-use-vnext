package engine

import (
	"time"

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

// textStage is the bounded cumulative evidence of one accepted default/true
// text action. It lives on operationState so replacing work.UI cannot erase an
// earlier primitive fact. It is not persisted as ordinary action history.
type textStage struct {
	focus             string
	write             string
	enter             string
	enterAttempted    bool
	displayID         int
	displayGeneration string
}

func newTextStage(session *sessionState) *textStage {
	if session == nil {
		return nil
	}
	return &textStage{
		focus:     textFocusNotAttempted,
		write:     protocol.TextInputNotExecuted,
		enter:     protocol.TextInputNotExecuted,
		displayID: session.Display.ID, displayGeneration: session.Display.Generation,
	}
}

// textOverallOutcome derives the conservative action outcome from the
// cumulative primitive evidence. executed requires both confirmed keys;
// not_executed requires that no primitive could have been dispatched.
func textOverallOutcome(stage *textStage) string {
	if stage == nil {
		return OutcomeNotExecuted
	}
	if stage.write == protocol.TextInputExecuted && stage.enter == protocol.TextInputExecuted {
		return OutcomeExecuted
	}
	focusMayHaveDispatched := stage.focus == textFocusUncertain || stage.focus == textFocusExecuted
	writeMayHaveDispatched := stage.write == protocol.TextInputUncertain ||
		stage.write == protocol.TextInputExecuted
	enterMayHaveDispatched := stage.enter == protocol.TextInputUncertain ||
		stage.enter == protocol.TextInputExecuted
	if !focusMayHaveDispatched && !writeMayHaveDispatched && !enterMayHaveDispatched {
		return OutcomeNotExecuted
	}
	return OutcomeUncertain
}

// initTextStageLocked publishes the initial (not_executed, not_executed)
// snapshot in the same critical section that claims the observation. The
// caller holds e.mu.
func (e *Engine) initTextStageLocked(op *operationState, session *sessionState) {
	if op == nil || op.TextStage != nil {
		return
	}
	stage := newTextStage(session)
	if stage == nil {
		return
	}
	op.TextStage = stage
	e.publishTextInputLocked(op)
}

// publishTextInputLocked replaces the public immutable snapshot. The caller
// holds e.mu.
func (e *Engine) publishTextInputLocked(op *operationState) {
	if op == nil || op.TextStage == nil {
		return
	}
	op.TextInput = &protocol.TextInputView{
		Write: op.TextStage.write, Enter: op.TextStage.enter,
	}
}

// markTextPrimitiveLocked advances one primitive. It is called only while the
// corresponding dispatch authorization is being granted or its receipt is
// being recorded. The caller holds e.mu and has already checked ownership.
func (e *Engine) markTextPrimitiveLocked(work *workState, primitive, state string) {
	if work == nil || primitive == "" {
		return
	}
	op := e.operations[work.OperationID]
	if op == nil || op.State != OperationRunning || op.TextStage == nil {
		return
	}
	switch primitive {
	case "focus":
		op.TextStage.focus = state
	case "write":
		op.TextStage.write = state
	case "enter":
		op.TextStage.enter = state
	default:
		return
	}
	e.publishTextInputLocked(op)
}

// setTextPrimitive applies one receipt-driven transition behind the full
// conditional gate. It never advances a frozen or replaced operation.
func (e *Engine) setTextPrimitive(work *workState, op *operationState, primitive, state string) bool {
	if op == nil || op.TextStage == nil || primitive == "" || state == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.State != OperationRunning || e.work != work ||
		!e.workAuthorizedLocked(work, time.Now()) || work.Ctx.Err() != nil ||
		!e.textStageAuthorizedLocked(work) {
		return false
	}
	switch primitive {
	case "focus":
		op.TextStage.focus = state
	case "write":
		op.TextStage.write = state
	case "enter":
		op.TextStage.enter = state
	default:
		return false
	}
	e.publishTextInputLocked(op)
	return true
}

// beginTextEnter records the one allowed ENTER attempt and checks the write
// stage, display identity and operation ownership before may_dispatch.
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

// writeTextExecuted publishes the confirmed write and the exact readback fact.
func (e *Engine) writeTextExecuted(work *workState, op *operationState) bool {
	return e.setTextPrimitive(work, op, "write", protocol.TextInputExecuted)
}
