package engine

import (
	"context"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// textPartialPhase is the fixed acquisition phase of the on-the-spot evidence a
// confirmed-partial text action delivers. It names only an internal delivery
// path: it never becomes a tool name and never grants a baseline confirmation.
const textPartialPhase = "act.partial"

// deliverTextPartialObservation turns the frame the write path already read into
// the fresh ready observation of a confirmed-partial text action. The write and
// its absence of submission are already confirmed, so this is an ordinary
// post-observation: it uses the same projector, the same publish commit gate and
// the same bounded pool fallback as every other acquisition, with
// allowPoolFallback=true and forceFull=false regardless of the original O's
// shape. It never runs for an image request and never reuses a frame that the
// delivery guards no longer accept; those cases fall back to one standard
// acquisition.
func (e *Engine) deliverTextPartialObservation(work *workState, session *sessionState,
	partial *device.Capture, wantImage bool) (*protocol.Observation, *protocol.APIError) {
	if !wantImage && partial != nil && partialForegroundUsable(partial.Foreground) {
		public, stopped := e.publishTextPartialCapture(work, session, *partial)
		if stopped != nil {
			return nil, stopped
		}
		if public != nil {
			return public, nil
		}
	}
	public, _, postErr := e.captureAndPublish(work.Ctx, work, session, false, wantImage, "act")
	return public, postErr
}

// partialForegroundUsable is the read-only delivery precondition of a reused
// after-write frame: only a present or absent-but-known foreground can stand on
// its own. An unknown foreground would make the delivered O unusable for the
// same target proof that rejected the previous one.
func partialForegroundUsable(foreground device.Foreground) bool {
	return foreground.State == device.ForegroundPresent ||
		foreground.State == device.ForegroundNone
}

// publishTextPartialCapture projects one already-read frame and publishes it as
// the session's current observation. It returns:
//   - (public, nil) when the frame was published;
//   - (nil, stopped) when the original work lost its authorization, so no
//     observation may be attached;
//   - (nil, nil) when the frame is not deliverable, which lets the caller take
//     one standard acquisition.
//
// The per-capture guards are the shared captureValidated source (capture age,
// target display binding, control-UI exclusion); no second copy of them exists
// here.
func (e *Engine) publishTextPartialCapture(work *workState, session *sessionState,
	capture device.Capture) (*protocol.Observation, *protocol.APIError) {
	if work == nil || session == nil {
		return nil, nil
	}
	if e.workRevoked(work) || work.Ctx == nil || work.Ctx.Err() != nil {
		return nil, stoppedActRevalidation()
	}
	// captureValidated stamps the session display onto the frame, so the frame
	// itself must first prove it was read from exactly that display instance.
	// A frame from another display, another generation or another geometry can
	// never be republished as this session's observation; the caller then takes
	// one standard fresh acquisition instead.
	if !textSubmitCaptureTrusted(&capture, session.Display) {
		return nil, nil
	}
	validated, apiErr := e.captureValidated(capture, session)
	if apiErr != nil {
		return nil, nil
	}
	tree, apiErr := e.projectCapture(work, textPartialPhase, validated)
	if apiErr != nil {
		if apiErr.Code == protocol.CodeStopped {
			return nil, apiErr
		}
		return nil, nil
	}
	// A delivered partial frame never settled: it is the single fresh frame the
	// write path already read, so its quality is the fixed not_sampled fact.
	quality := &protocol.Quality{
		State: protocol.QualityUnknown, Samples: 0,
		StopReason: protocol.QualityStopNotSampled,
	}
	public, _, publishErr := e.publishTreeAttempt(work, session, textPartialPhase, tree,
		tree.capture.CapturedAt, true, false, "complete", nil, quality)
	e.logQuality(work.OperationID, textPartialPhase, public, quality)
	if publishErr != nil {
		if publishErr.Code == protocol.CodeStopped {
			return nil, publishErr
		}
		return nil, nil
	}
	if public == nil || public.ID == "" || public.Status == "failed" {
		return nil, nil
	}
	e.logDelivery(work, "published_partial", "", treeFormat(public))
	return public, nil
}

// finishPartialText is the single Act-level completion of the confirmed-partial
// text fact. The write and its definite absence of submission are already
// committed on the operation; this path only clears the dispatch-unknown mark,
// delivers the on-the-spot evidence while the original authority still holds,
// and saves the one semantic recovery base. STOP wins at every boundary: a
// revoked work never publishes and reports control_revoked, never a text
// submission code.
func (e *Engine) finishPartialText(work *workState, op *operationState,
	session *sessionState, partialCapture *device.Capture,
	partialErr *protocol.APIError, wantImage bool) *protocol.Result {
	e.clearDispatchUnknown(work)
	e.mu.Lock()
	revoked, deadline := e.partialTextAuthorityLocked(work, time.Now())
	e.mu.Unlock()
	var public *protocol.Observation
	if !revoked && !deadline {
		var postErr *protocol.APIError
		public, postErr = e.deliverTextPartialObservation(work, session, partialCapture, wantImage)
		switch {
		case postErr != nil && postErr.Code == protocol.CodeStopped:
			public = nil
		case postErr != nil && public == nil:
			// The write fact stays confirmed; the post observation reports only
			// the failed tree it could not read, never a false success. This
			// placeholder has no identity, so it cannot suppress recovery.
			public = &protocol.Observation{
				Status: "failed", Missing: []string{"tree"}, Error: postErr.Public(),
			}
		}
	}
	return e.finishPartialTerminal(work, op, public, partialErr)
}

// finishPartialTerminal saves the one terminal fact of a confirmed-partial text
// action. The final-authority check, the delivered-observation acceptance and
// finishOperationLocked share one short lock, so a STOP that lands between the
// post-observation commit and this save cannot be downgraded into an ordinary
// text timeout, an active session whose own work budget expired is still the
// text_submit_not_executed partial fact, and a frame that lost current authority
// is never attached to a live answer. No second authority is introduced: the
// decision reads only the existing work/session/current facts and stores them
// through the ordinary operation terminal.
func (e *Engine) finishPartialTerminal(work *workState, op *operationState,
	public *protocol.Observation, partialErr *protocol.APIError) *protocol.Result {
	if op == nil {
		return &protocol.Result{Outcome: OutcomePartial}
	}
	if partialErr == nil || partialErr.Code == "" {
		partialErr = e.textError(op, 409, protocol.CodeTextSubmitNotExecuted,
			"文字已写入，但提交未执行", textStageSubmit,
			textReasonSubmitTargetUnconfirmed, OutcomePartial, nil)
	}
	partialErr.Outcome = OutcomePartial
	if partialErr.HTTPStatus == 0 {
		partialErr.HTTPStatus = 409
	}

	now := time.Now()
	e.mu.Lock()
	revokedTerminal := false
	if op.State == OperationFinished {
		// A freeze that already won keeps its frozen facts and never accepts a
		// later observation; applyFrozenResult would drop it in any case.
		public = nil
		revokedTerminal = op.Error != nil && op.Error.Code == protocol.CodeStopped
	} else {
		revoked, deadline := e.partialTextAuthorityLocked(work, now)
		phase, errToSave := "partial", partialErr
		switch {
		case revoked:
			phase = "partial_revoked"
			revokedTerminal = true
			public = nil
			errToSave = e.textErrorLocked(op, 409, protocol.CodeStopped, "原工作已撤销",
				textStageSubmit, textReasonAuthorizationRevoked, OutcomePartial, nil)
		case deadline:
			// The original session is still the live authority; only this
			// action's own work budget ran out. That is still the confirmed
			// partial fact, not a result-unknown write, and the frozen matrix
			// publishes no observation for it.
			public = nil
			errToSave = e.textErrorLocked(op, 409, protocol.CodeTextSubmitNotExecuted,
				"文字已写入，提交未执行", textStageSubmit,
				textReasonDeadlineExceeded, OutcomePartial, nil)
		}
		if !revokedTerminal && !deadline && public != nil &&
			public.ID != "" && public.Status != "failed" &&
			e.partialDeliveryReadyLocked(op, public, now) {
			op.Delivered = true
		}
		e.finishOperationLocked(op, OutcomePartial, phase, errToSave)
	}
	outcome := op.Outcome
	recovery := op.Recovery
	var savedErr *protocol.Error
	if op.Error != nil {
		cloned := *op.Error
		cloned.Details = protocol.CloneErrorDetails(op.Error.Details)
		savedErr = &cloned
	}
	e.mu.Unlock()

	result := e.newResult(op)
	result.Outcome = outcome
	if savedErr != nil {
		result.Error = savedErr
	}
	if !revokedTerminal && public != nil {
		result.Observation = public
	}
	result.Recovery = recovery
	if revokedTerminal {
		e.addSessionIfEnded(result)
	}
	return result
}

// partialTextAuthorityLocked classifies the original action's live authority for
// a confirmed-partial terminal. Revocation (an ended/switched session, a lost
// work position or a cancelled context) wins; an active same-generation session
// whose own work budget expired is the bounded timeout, never a revocation. The
// caller holds e.mu. Device readiness is deliberately not part of this
// classification: it is a live recovery overlay, not a terminal fact.
func (e *Engine) partialTextAuthorityLocked(work *workState, now time.Time) (revoked, deadline bool) {
	if work == nil {
		return true, false
	}
	session := e.session
	sessionLive := session != nil && session.State != SessionEnded &&
		now.Before(session.ExpiresAt) && session.Epoch == work.SessionEpoch &&
		session.AppInstanceID == e.app.InstanceID &&
		session.ModeGeneration == e.app.ModeGeneration && session.Mode == e.app.Mode
	if e.work != work || work.Revoked || e.shuttingDown || !sessionLive ||
		!e.textStageAuthorizedLocked(work) {
		return true, false
	}
	if work.Ctx != nil {
		switch work.Ctx.Err() {
		case nil:
		case context.DeadlineExceeded:
			return false, true
		default:
			return true, false
		}
	}
	if !work.Deadline.IsZero() && !now.Before(work.Deadline) {
		return false, true
	}
	return false, false
}
