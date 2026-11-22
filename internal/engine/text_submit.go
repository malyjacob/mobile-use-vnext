package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// textSubmitRetryDelay bounds the one post-write re-capture a node submit_ref
// may use. It covers an App enabling a greyed button and the keyboard animation
// settling without turning a definite rejection into a retry loop.
const textSubmitRetryDelay = 150 * time.Millisecond

// textSubmitMaxCaptures is the first post-write frame plus at most one retry.
const textSubmitMaxCaptures = 2

// textSubmitPlan is the immutable static plan of one text submit_ref node.
// initiallyDisabled records whether the actual execution target was explicitly
// disabled before the write: only that fact permits (and requires) a fresh
// disabled->enabled transition after the write.
type textSubmitPlan struct {
	ref               string
	tap               *tapPlan
	initiallyDisabled bool
}

// planTextSubmitRef statically plans the submit_ref tap through the complete
// tap planner with the bubble=true semantics. It runs before claiming the
// observation, performs no device I/O, and is the only place that relaxes the
// initial enabled gate for a text submit target.
func planTextSubmitRef(obs *observationState,
	operation *protocol.Operation) (*textSubmitPlan, *protocol.APIError) {
	if operation == nil || operation.Type != "text" || !operation.IsSubmitTap() {
		return nil, nil
	}
	ref := operation.EffectiveSubmitRef()
	if ref == "" || ref == protocol.SubmitRefEnterKey {
		return nil, nil
	}
	// A synthetic tap operation reuses the one static tap planner: bubble is
	// effectively true for submit, and allowInitiallyDisabled is the only
	// relaxed gate. Every other structural/capability/safe-point check is
	// identical to an ordinary ref tap.
	submitOp := &protocol.Operation{
		Type:      "tap",
		Target:    &protocol.Target{UseRef: true, Ref: ref},
		Bubble:    true,
		HasBubble: true,
	}
	_, _, plan, apiErr := staticTapRefCheckWithOptions(obs, submitOp,
		tapPlanOptions{allowInitiallyDisabled: true})
	if apiErr != nil {
		return nil, apiErr
	}
	if plan == nil {
		return nil, tapHierarchyError()
	}
	return &textSubmitPlan{
		ref:               ref,
		tap:               plan,
		initiallyDisabled: tapPlanInitiallyDisabled(plan),
	}, nil
}

// tapPlanInitiallyDisabled reports the explicit pre-write disabled fact of the
// actual execution target. Unknown enabled state is never treated as disabled.
func tapPlanInitiallyDisabled(plan *tapPlan) bool {
	raw := tapPlanExecutionRaw(plan)
	return raw != nil && raw.HasEnabled && !raw.Enabled
}

// sameVerticalBandResize is the post-write rebind geometry for a field whose
// top and bottom barely moved while its horizontal extent shrank or grew (the
// inline clear button appearing inside a search box). It is deliberately
// narrower than sameHorizontalBand: only the X extent may change materially.
func sameVerticalBandResize(a, b device.Rect) bool {
	if a.Width() <= 0 || b.Width() <= 0 || a.Height() <= 0 || b.Height() <= 0 {
		return false
	}
	tolY := max(8, max(a.Height(), b.Height())/5)
	if absInt(a.Top-b.Top) > tolY || absInt(a.Bottom-b.Bottom) > tolY {
		return false
	}
	if absInt(a.Height()-b.Height()) > tolY {
		return false
	}
	inter := a.Intersect(b)
	if inter.Empty() {
		return false
	}
	return inter.Width()*2 >= min(a.Width(), b.Width())
}

// textWriteCandidateSafe is the post-write identity/state filter for the one
// rebound input. It is stricter than textCandidateSafe because a fresh capture
// after the write must still prove focus, enablement and a non-password field.
func textWriteCandidateSafe(node *device.RawNode) bool {
	return node != nil && node.Visible && node.Editable &&
		node.HasFocused && node.Focused &&
		node.HasEnabled && node.Enabled &&
		node.HasPassword && !node.Password
}

// textWriteAnchorCount counts the same-display/package/class editable nodes
// that carry the exact anchor field named by want. It compares the chosen
// field directly instead of re-deriving an anchor precedence: a description
// anchor stays a description anchor even when the fresh frame adds a
// resource-id to the same field.
func textWriteAnchorCount(capture *device.Capture, want *device.RawNode,
	kind string, anchor optionalString) int {
	if capture == nil || want == nil || capture.Display.ID != want.DisplayID {
		return 0
	}
	count := 0
	for index := range capture.Nodes {
		node := &capture.Nodes[index]
		if node.DisplayID != want.DisplayID || !node.Editable ||
			node.Package != want.Package || node.ClassName != want.ClassName {
			continue
		}
		switch kind {
		case "resource":
			if node.ResourceSet && node.ResourceID == anchor.value {
				count++
			}
		case "description":
			if node.DescSet && node.Description == anchor.value {
				count++
			}
		}
	}
	return count
}

// textWriteAnchorMatch applies the direct field comparison of the chosen
// anchor to one post-write candidate.
func textWriteAnchorMatch(node *device.RawNode, kind string, anchor optionalString) bool {
	if node == nil {
		return false
	}
	switch kind {
	case "resource":
		return node.ResourceSet && node.ResourceID == anchor.value
	case "description":
		return node.DescSet && node.Description == anchor.value
	}
	return false
}

// textWriteGeometryCompatible is the three-way post-write geometry gate:
// keyboard vertical shift, inline resize, or a plain overlap.
func textWriteGeometryCompatible(oldBounds, newBounds device.Rect) bool {
	return sameHorizontalBand(oldBounds, newBounds) ||
		sameVerticalBandResize(oldBounds, newBounds) ||
		overlapRatio(oldBounds, newBounds) >= 0.5
}

// textWriteBoundsUsable requires a positive-area rectangle with at least one
// clipped pixel on the target screen. A focused field may be partially clipped
// by the keyboard, so full containment is not required for the write-after
// anchor; a rectangle with no on-screen intersection can never name it.
func textWriteBoundsUsable(bounds, screen device.Rect) bool {
	if bounds.Empty() || screen.Empty() {
		return false
	}
	return !bounds.Intersect(screen).Empty()
}

// rebindPostWriteTextTarget performs the single bounded write-after rebind of
// the input that carried the confirmed write. Anchor uniqueness is counted in
// both the pre-write frame and the fresh frame so two same-shaped fields can
// never be disambiguated by the write itself. Without an anchor, the global
// count of safe same-source fields with the exact text is decided before any
// geometry is considered, so a far-away duplicate can never be filtered away by
// distance. Empty text is a legal write value and is treated like any other.
func rebindPostWriteTextTarget(before *device.Capture, want *device.RawNode,
	afterWrite *device.Capture, text string) (*device.RawNode, bool) {
	if before == nil || want == nil || afterWrite == nil ||
		before.Display.ID != afterWrite.Display.ID ||
		want.DisplayID != afterWrite.Display.ID {
		return nil, false
	}
	screen := afterWrite.Display.Bounds()
	if !textWriteBoundsUsable(want.Bounds, screen) {
		return nil, false
	}
	kind, anchor, hasAnchor := stableTextAnchor(want)
	if hasAnchor {
		if textWriteAnchorCount(before, want, kind, anchor) != 1 ||
			textWriteAnchorCount(afterWrite, want, kind, anchor) != 1 {
			return nil, false
		}
	}
	var matched []*device.RawNode
	for i := range afterWrite.Nodes {
		node := &afterWrite.Nodes[i]
		if node.DisplayID != want.DisplayID || !textWriteCandidateSafe(node) ||
			node.Package != want.Package || node.ClassName != want.ClassName ||
			!node.TextSet || node.Text != text ||
			!textWriteBoundsUsable(node.Bounds, screen) {
			continue
		}
		if hasAnchor && !textWriteAnchorMatch(node, kind, anchor) {
			continue
		}
		if hasAnchor && !textWriteGeometryCompatible(want.Bounds, node.Bounds) {
			continue
		}
		matched = append(matched, node)
	}
	if len(matched) != 1 {
		return nil, false
	}
	if !hasAnchor && !textWriteGeometryCompatible(want.Bounds, matched[0].Bounds) {
		return nil, false
	}
	return matched[0], true
}

// textSubmitCaptureTrusted requires the post-write frame to carry the exact
// display identity the submission was authorized for. A frame from another
// display instance or with different dimensions cannot name the same window.
func textSubmitCaptureTrusted(capture *device.Capture, display device.Display) bool {
	return capture != nil && capture.Display.ID == display.ID &&
		capture.Display.Generation == display.Generation &&
		capture.Display.Width == display.Width &&
		capture.Display.Height == display.Height
}

// textSubmitDisplayCurrent compares the admission-time display snapshot with
// the live session display. A recreated or switched display invalidates every
// pending submission primitive: the capture adapter stamps the requested
// display, so only the live session can prove the generation did not change.
func (e *Engine) textSubmitDisplayCurrent(work *workState, display device.Display) bool {
	current := e.sessionForWork(work)
	return current != nil && current.Display.ID == display.ID &&
		current.Display.Generation == display.Generation &&
		current.Display.Width == display.Width &&
		current.Display.Height == display.Height
}

// textSubmitError builds the one controlled partial submission error. The
// confirmed write stays executed; only the submission is not_executed.
func (e *Engine) textSubmitError(op *operationState, reason string, count *int) *protocol.APIError {
	return e.textError(op, 409, protocol.CodeTextSubmitNotExecuted,
		"文字已写入，但提交未执行", textStageSubmit, reason, OutcomePartial, count)
}

// textSubmitStopped is the STOP-first projection of a confirmed write whose
// submission definitely never left the engine.
func (e *Engine) textSubmitStopped(op *operationState, capture *device.Capture) (string, *device.Capture, *protocol.APIError) {
	return OutcomePartial, capture, e.textError(op, 409, protocol.CodeStopped,
		"原工作已撤销", textStageSubmit, textReasonAuthorizationRevoked,
		OutcomePartial, nil)
}

// textSubmitWorkUsable re-checks the original work immediately before one
// authorized submission step. A revoked or cancelled context always wins.
func (e *Engine) textSubmitWorkUsable(work *workState) bool {
	return work != nil && !e.workRevoked(work) && work.Ctx != nil && work.Ctx.Err() == nil
}

// dispatchTextEnterSubmit runs the enter_key submission path. The write is
// already confirmed. It performs one foreground check, one exact read of the
// bound input, at most one post-write capture rebind when the read proves the
// old geometry no longer names the field, one more exact read, and then at most
// one ENTER. It never sends a second submission primitive.
func (e *Engine) dispatchTextEnterSubmit(work *workState, op *operationState, session *sessionState,
	obs *observationState, bound textBinding, text string) (string, *device.Capture, *protocol.APIError) {
	if !e.textSubmitWorkUsable(work) {
		return e.textSubmitStopped(op, nil)
	}
	if !e.setTextStage(work, op, textStageSubmit) {
		return e.textSubmitStageLost(work, op, nil)
	}
	foreground, foregroundErr := e.dev.Foreground(work.Ctx, session.Display.ID)
	if foregroundErr != nil || !sameTextTaskContext(obs.Foreground, foreground) {
		if !e.textSubmitWorkUsable(work) {
			return e.textSubmitStopped(op, nil)
		}
		return OutcomePartial, nil, e.textSubmitError(op,
			textReasonSubmitContextUnconfirmed, nil)
	}
	if !e.textSubmitWorkUsable(work) {
		return e.textSubmitStopped(op, nil)
	}

	read, readErr := e.dev.GetTextOnDisplay(work.Ctx, textTargetOf(bound.raw, session.Display))
	if readErr == nil && read.CandidateCount == 1 && read.Value != nil && *read.Value == text {
		if !e.textSubmitWorkUsable(work) {
			return e.textSubmitStopped(op, nil)
		}
		return e.dispatchSubmitKey(work, op, session, nil)
	}
	var count *int
	if readErr == nil {
		c := read.CandidateCount
		count = &c
	}
	if readErr != nil || read.CandidateCount != 0 {
		// A transport error or any count other than the one rebindable zero
		// remains an unconfirmed submit target. Nothing is dispatched.
		if !e.textSubmitWorkUsable(work) {
			return e.textSubmitStopped(op, nil)
		}
		return OutcomePartial, nil, e.textSubmitError(op,
			textReasonSubmitTargetUnconfirmed, count)
	}

	afterWrite, captureErr := e.dev.Capture(work.Ctx, session.Display)
	if captureErr != nil {
		if !e.textSubmitWorkUsable(work) {
			return e.textSubmitStopped(op, nil)
		}
		zero := 0
		return OutcomePartial, nil, e.textSubmitError(op,
			textReasonSubmitTargetUnconfirmed, &zero)
	}
	if !e.textSubmitWorkUsable(work) {
		return e.textSubmitStopped(op, &afterWrite)
	}
	if !textSubmitCaptureTrusted(&afterWrite, session.Display) ||
		!e.textSubmitDisplayCurrent(work, session.Display) {
		return OutcomePartial, &afterWrite, e.textSubmitError(op,
			textReasonSubmitContextUnconfirmed, nil)
	}
	if _, topologyErr := validateTapRawTopology(&afterWrite); topologyErr != nil {
		return OutcomePartial, &afterWrite, e.textSubmitError(op,
			textReasonSubmitTargetUnconfirmed, nil)
	}
	if !sameTextTaskContext(obs.Foreground, afterWrite.Foreground) {
		return OutcomePartial, &afterWrite, e.textSubmitError(op,
			textReasonSubmitContextUnconfirmed, nil)
	}
	rebound, ok := rebindPostWriteTextTarget(bound.capture, bound.raw, &afterWrite, text)
	if !ok {
		zero := 0
		return OutcomePartial, &afterWrite, e.textSubmitError(op,
			textReasonSubmitTargetUnconfirmed, &zero)
	}
	if !e.textSubmitWorkUsable(work) {
		return e.textSubmitStopped(op, &afterWrite)
	}
	read2, read2Err := e.dev.GetTextOnDisplay(work.Ctx,
		textTargetOf(rebound, session.Display))
	if read2Err != nil || read2.CandidateCount != 1 || read2.Value == nil || *read2.Value != text {
		if !e.textSubmitWorkUsable(work) {
			return e.textSubmitStopped(op, &afterWrite)
		}
		var count2 *int
		if read2Err == nil {
			c := read2.CandidateCount
			count2 = &c
		}
		return OutcomePartial, &afterWrite, e.textSubmitError(op,
			textReasonSubmitTargetUnconfirmed, count2)
	}
	if !e.textSubmitWorkUsable(work) {
		return e.textSubmitStopped(op, &afterWrite)
	}
	return e.dispatchSubmitKey(work, op, session, &afterWrite)
}

// dispatchTextSubmitTap runs the node submit_ref path. Each attempt reads one
// fresh frame, proves its window context, revalidates the complete tap plan on
// that frame, and only a fully fresh safe point may be tapped. A failed attempt
// is retried at most once after the fixed short delay, and only for reasons the
// proposal classifies as an unsettled post-write frame.
func (e *Engine) dispatchTextSubmitTap(work *workState, op *operationState, session *sessionState,
	obs *observationState, submit *textSubmitPlan) (string, *device.Capture, *protocol.APIError) {
	if submit == nil || submit.tap == nil {
		return OutcomePartial, nil, e.textSubmitError(op,
			textReasonSubmitTargetUnconfirmed, nil)
	}
	if !e.textSubmitWorkUsable(work) {
		return e.textSubmitStopped(op, nil)
	}
	if !e.setTextStage(work, op, textStageSubmit) {
		return e.textSubmitStageLost(work, op, nil)
	}
	var lastCapture *device.Capture
	for attempt := 0; attempt < textSubmitMaxCaptures; attempt++ {
		if attempt > 0 {
			if !pauseInterruptible(work.Ctx, textSubmitRetryDelay) ||
				!e.textSubmitWorkUsable(work) {
				return e.textSubmitStopped(op, lastCapture)
			}
		}
		afterWrite, captureErr := e.dev.Capture(work.Ctx, session.Display)
		if captureErr != nil {
			if !e.textSubmitWorkUsable(work) {
				return e.textSubmitStopped(op, lastCapture)
			}
			return OutcomePartial, lastCapture, e.textSubmitError(op,
				textReasonSubmitTargetUnconfirmed, nil)
		}
		lastCapture = &afterWrite
		if !e.textSubmitWorkUsable(work) {
			return e.textSubmitStopped(op, lastCapture)
		}
		if !textSubmitCaptureTrusted(&afterWrite, session.Display) ||
			!e.textSubmitDisplayCurrent(work, session.Display) {
			return OutcomePartial, lastCapture, e.textSubmitError(op,
				textReasonSubmitContextUnconfirmed, nil)
		}
		if !sameTextTaskContext(obs.Foreground, afterWrite.Foreground) {
			return OutcomePartial, lastCapture, e.textSubmitError(op,
				textReasonSubmitContextUnconfirmed, nil)
		}
		frame, apiErr := e.revalidateSubmitTapFrame(work.Ctx, func() bool {
			return e.workRevoked(work)
		}, work.Projector, obs, &afterWrite, submit.tap, submit.initiallyDisabled)
		if apiErr == nil && frame != nil {
			return e.dispatchSubmitTapPrimitive(work, op, session, frame, lastCapture)
		}
		if !e.textSubmitWorkUsable(work) ||
			(apiErr != nil && apiErr.Code == protocol.CodeStopped) {
			return e.textSubmitStopped(op, lastCapture)
		}
		reason, retryable := textSubmitRevalidationReason(apiErr)
		if !retryable || attempt+1 >= textSubmitMaxCaptures {
			return OutcomePartial, lastCapture, e.textSubmitError(op, reason, nil)
		}
	}
	return OutcomePartial, lastCapture, e.textSubmitError(op,
		textReasonSubmitTargetUnconfirmed, nil)
}

// textSubmitStageLost uses the retained primitive facts when an authorization
// check fails. A display change can invalidate the text binding before the
// work position itself is revoked.
func (e *Engine) textSubmitStageLost(work *workState, op *operationState,
	capture *device.Capture) (string, *device.Capture, *protocol.APIError) {
	if e.textFailureOutcome(op) == OutcomePartial {
		if !e.textSubmitWorkUsable(work) {
			return e.textSubmitStopped(op, capture)
		}
		return OutcomePartial, capture, e.textSubmitError(op, textReasonSubmitContextUnconfirmed, nil)
	}
	return OutcomeUncertain, nil, e.textError(op, 504, protocol.CodeActionUnknown,
		"提交结果无法确认", textStageSubmit, textReasonSubmitUnconfirmed,
		OutcomeUncertain, nil)
}

// textSubmitRevalidationReason maps the fresh-frame revalidation result to the
// closed submit reasons. Only a frame that is still settling may be recaptured:
// scope/context/structural/STOP failures are definite and never retried.
func textSubmitRevalidationReason(apiErr *protocol.APIError) (string, bool) {
	if apiErr == nil {
		return textReasonSubmitTargetUnconfirmed, false
	}
	if apiErr.Code == protocol.CodeStopped {
		return textReasonAuthorizationRevoked, false
	}
	reason := ""
	if apiErr.Details != nil {
		if value, ok := apiErr.Details["reason"].(string); ok {
			reason = value
		}
	}
	switch reason {
	case targetChangedReasonScopeBlocked:
		return textReasonSubmitScopeBlocked, false
	case submitFrameReasonDisabled:
		return textReasonSubmitDisabled, true
	}
	if apiErr.Code == protocol.CodeTargetChanged ||
		apiErr.Code == protocol.CodeNoSafePoint {
		return textReasonSubmitTargetUnconfirmed, true
	}
	return textReasonSubmitTargetUnconfirmed, false
}

// dispatchSubmitKey sends the one bounded ENTER submission. Every pre-dispatch
// gate has already passed; a definite non-dispatch is a partial, a dispatched
// but unconfirmed receipt is uncertain, and no path replays the key.
func (e *Engine) dispatchSubmitKey(work *workState, op *operationState,
	session *sessionState, capture *device.Capture) (string, *device.Capture, *protocol.APIError) {
	if !e.textSubmitWorkUsable(work) {
		return e.textSubmitStopped(op, capture)
	}
	if !e.beginTextSubmit(work, op) {
		return e.textSubmitStageLost(work, op, capture)
	}
	if apiErr := e.beginUIDispatchStage(work, "key", "submit"); apiErr != nil {
		if apiErr.Code == protocol.CodeStopped || !e.textSubmitWorkUsable(work) {
			return e.textSubmitStopped(op, capture)
		}
		return OutcomePartial, capture, e.textSubmitError(op,
			textReasonSubmitNotSent, nil)
	}
	receipt, keyErr := e.dev.Key(work.Ctx, session.Display.ID, "ENTER")
	return e.completeTextSubmit(work, op, capture, receipt, keyErr)
}

// dispatchSubmitTapPrimitive sends the one bounded submit_ref tap at the fresh
// safe point. The tap is never replayed and never followed by an ENTER.
func (e *Engine) dispatchSubmitTapPrimitive(work *workState, op *operationState,
	session *sessionState, frame *tapFrame, capture *device.Capture) (string, *device.Capture, *protocol.APIError) {
	if frame == nil {
		return OutcomePartial, capture, e.textSubmitError(op,
			textReasonSubmitTargetUnconfirmed, nil)
	}
	if !e.textSubmitWorkUsable(work) {
		return e.textSubmitStopped(op, capture)
	}
	if !e.beginTextSubmit(work, op) {
		return e.textSubmitStageLost(work, op, capture)
	}
	if apiErr := e.beginUIDispatchStage(work, "tap", "submit"); apiErr != nil {
		if apiErr.Code == protocol.CodeStopped || !e.textSubmitWorkUsable(work) {
			return e.textSubmitStopped(op, capture)
		}
		return OutcomePartial, capture, e.textSubmitError(op,
			textReasonSubmitNotSent, nil)
	}
	receipt, tapErr := e.dev.Tap(work.Ctx, session.Display.ID, frame.x, frame.y)
	return e.completeTextSubmit(work, op, capture, receipt, tapErr)
}

// completeTextSubmit records the one physical submission receipt. The public
// facts are committed in the same order for ENTER and tap.
func (e *Engine) completeTextSubmit(work *workState, op *operationState,
	capture *device.Capture, receipt device.DispatchReceipt,
	dispatchErr error) (string, *device.Capture, *protocol.APIError) {
	e.recordUIReceipt(work, receipt)
	if receipt == device.DispatchNotDispatched {
		if !e.commitTextReceipt(work, op, "submit", receipt, dispatchErr) {
			return e.textSubmitStageLost(work, op, capture)
		}
		return OutcomePartial, capture, e.textSubmitError(op,
			textReasonSubmitNotSent, nil)
	}
	if receipt == device.DispatchTerminated && dispatchErr == nil {
		e.commitTextReceipt(work, op, "submit", receipt, nil)
		if e.confirmTextSubmit(work, op) {
			return OutcomeExecuted, nil, nil
		}
		return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageSubmit, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	e.commitTextReceipt(work, op, "submit", receipt, dispatchErr)
	return OutcomeUncertain, capture, e.textError(op, 504, protocol.CodeActionUnknown,
		"提交已派发，但完成结果无法确认", textStageSubmit,
		textReasonSubmitUnconfirmed, OutcomeUncertain, nil)
}
