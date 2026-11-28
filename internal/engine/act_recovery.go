package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func cloneAutoRecovery(recovery *protocol.AutoRecoveryView) *protocol.AutoRecoveryView {
	if recovery == nil {
		return nil
	}
	copy := *recovery
	return &copy
}

func actOriginalRef(operation *protocol.Operation) string {
	target := operation.Target
	if operation.Type == "scroll" {
		target = operation.Container
	}
	if target != nil && target.UseRef {
		return target.Ref
	}
	return ""
}

func hasActRevalidationDetails(kind string) bool {
	switch kind {
	case "tap", "long_press", "text", "scroll":
		return true
	}
	return false
}

func (e *Engine) actRevalidationAuthorizedLocked(work *workState, op *operationState,
	obs *observationState, now time.Time) bool {
	return op.State == OperationRunning && e.work == work && e.workAuthorizedLocked(work, now) &&
		work.Ctx.Err() == nil && (work.Deadline.IsZero() || now.Before(work.Deadline)) &&
		e.session.Display.ID == obs.DisplayID && e.session.Display.Generation == obs.DisplayGeneration
}

func (e *Engine) actRevalidationAuthorized(work *workState, op *operationState, obs *observationState) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.actRevalidationAuthorizedLocked(work, op, obs, time.Now())
}

// commitActDispatch saves the recovery fact at the same boundary that admits
// the dispatch phase. Individual primitives still pass their own dispatch gates.
func (e *Engine) commitActDispatch(work *workState, op *operationState, obs *observationState,
	recovery *protocol.AutoRecoveryView) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.actRevalidationAuthorizedLocked(work, op, obs, time.Now()) {
		return false
	}
	op.Phase = "dispatch"
	op.AutoRecovery = cloneAutoRecovery(recovery)
	return true
}

func canRebindActTarget(kind, ref, reason string, tap *tapPlan) bool {
	if ref == "" || tap == nil || (reason != targetChangedReasonTargetMissing &&
		reason != targetChangedReasonLineageChanged) {
		return false
	}
	switch kind {
	case "text":
		return true
	case "tap", "long_press":
		return tap.bubbleAncestorRaw == nil && tap.colocatedRaw == nil && tap.delegateRaw == nil
	}
	return false
}

func (e *Engine) rebindActTarget(work *workState, op *operationState, obs *observationState,
	fresh *device.Capture, tap *tapPlan, kind, ref, reason string) (actRebindDecision, *protocol.APIError) {
	if fresh == nil {
		return actRebindDecision{}, nil
	}
	originalDisplay := obs.Projection.Capture.Display
	check := func() *protocol.APIError {
		if !e.actRevalidationAuthorized(work, op, obs) || fresh.Display.ID != originalDisplay.ID ||
			fresh.Display.Generation != originalDisplay.Generation {
			return stoppedActRevalidation()
		}
		return foregroundMatches(obs.Foreground, fresh.Foreground)
	}
	if apiErr := check(); apiErr != nil {
		return actRebindDecision{}, apiErr
	}
	origView, _ := collectTapPublicView(obs)
	var freshView *tapPublicView
	if work.Projector != nil {
		if preview, err := work.Projector.Preview(fresh); err == nil && preview != nil {
			freshView, _ = buildTapPublicView(preview.Tree, fresh, preview.Source)
		}
	}
	if apiErr := check(); apiErr != nil {
		return actRebindDecision{}, apiErr
	}
	decision, apiErr := planActRebind(actRebindRequest{
		Kind: kind, Reason: reason, TargetRef: ref,
		Obs: obs, OrigView: origView, OrigRaw: tap.targetRaw, OrigPlan: tap,
		Fresh: fresh, FreshView: freshView,
	})
	if boundaryErr := check(); boundaryErr != nil {
		return actRebindDecision{}, boundaryErr
	}
	return decision, apiErr
}

func (e *Engine) finishActRevalidation(work *workState, op *operationState, session *sessionState,
	obs *observationState, fresh *device.Capture, operation *protocol.Operation, tap *tapPlan,
	scroll *scrollPlan, apiErr *protocol.APIError, originalRef string, attempted []string) *protocol.Result {
	if !e.actRevalidationAuthorized(work, op, obs) {
		apiErr = stoppedActRevalidation()
	}
	unified := apiErr.Code == protocol.CodeTargetChanged && hasActRevalidationDetails(operation.Type)
	if unified {
		apiErr.WithDetail("phase", "revalidate").WithDetail("kind", operation.Type)
		if originalRef != "" {
			apiErr.WithDetail("original_ref", originalRef)
		}
		if len(attempted) > 0 {
			apiErr.WithDetail("recovery_attempted", attempted)
		}
	}
	apiErr.Outcome = OutcomeNotExecuted
	var public *protocol.Observation
	var published *observationState
	if deliverableRejection(apiErr.Code) {
		public, published = deliverRejection(e, work, session, obs, fresh, apiErr.Code)
	}
	if public != nil {
		e.markDelivered(op)
	} else if !e.actRevalidationAuthorized(work, op, obs) {
		apiErr = stoppedActRevalidation()
	}
	result := e.finishWithError(op, "revalidate", apiErr)
	result.Observation = public
	if unified && public != nil && published != nil {
		e.attachActRejectionDiagnostics(work.Ctx, result.Error, obs, published, operation, tap, scroll, originalRef)
	}
	return result
}
