package engine

import (
	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// deliverRejection publishes the frame a rejected act revalidation already read
// and returns its public projection, or nil when the rejection is not eligible.
//
// It is a variable only so the reverse-control test can prove the act path
// derives the delivery here and nowhere else. Production never assigns it.
var deliverRejection = (*Engine).deliverRejectedObservation

// deliverRejectedObservation turns a post-claim, pre-dispatch rejection into
// fresh evidence for the caller. Nothing is dispatched and no baseline is
// confirmed: the delivered observation is read-only material for the next
// request.
//
// Delivery applies only when a usable frame was read and can stand on its own. A
// missing frame, an unknown foreground, the control UI on the target display, a
// projection or size failure, or a failed commit gate all leave the result
// exactly as it was before this path existed.
func (e *Engine) deliverRejectedObservation(work *workState, session *sessionState,
	obs *observationState, fresh *device.Capture, code string) *protocol.Observation {
	if obs == nil || session == nil || fresh == nil || !deliverableRejection(code) {
		e.logDelivery(work, "skipped", code, "")
		return nil
	}
	// The next act must be able to prove the same foreground; delivering a frame
	// whose foreground is unknown would only produce another rejection.
	if fresh.Foreground.State != device.ForegroundPresent &&
		fresh.Foreground.State != device.ForegroundNone {
		e.logDelivery(work, "skipped", code, "")
		return nil
	}
	if session.Display.ID != 0 {
		for _, node := range fresh.Nodes {
			if node.Package == e.cfg.ControlPackage && node.Visible {
				e.logDelivery(work, "skipped", code, "")
				return nil
			}
		}
	}
	tree, apiErr := e.projectCapture(work, "act.delivery", *fresh)
	if apiErr != nil {
		e.logDelivery(work, "failed", apiErr.Code, "")
		return nil
	}
	// A delivered frame never settled: it is the single fresh frame the
	// revalidation already read, so its quality is the fixed not_sampled fact.
	quality := &protocol.Quality{
		State: protocol.QualityUnknown, Samples: 0,
		StopReason: protocol.QualityStopNotSampled,
	}
	// A full reference was confirmed at claim time, so its delivery may only use
	// that exact base. A delta reference already carried its full base in the
	// result; the delivered frame must not select a different historical base.
	selection := treeSelection{forceFull: !obs.Full}
	if obs.Full {
		selection = treeSelection{allowedBaseID: obs.ID}
	}
	public, _, publishErr := e.publishTreeAttempt(work, session, "act.delivery", tree,
		tree.capture.CapturedAt, selection, "complete", nil, quality)
	if publishErr != nil {
		e.logQuality(work.OperationID, "act.delivery", nil, quality)
		e.logDelivery(work, "failed", publishErr.Code, "")
		return nil
	}
	e.logQuality(work.OperationID, "act.delivery", public, quality)
	e.logDelivery(work, "published", code, treeFormat(public))
	return public
}

// deliverableRejection lists the rejection codes whose frame is worth handing
// back. The two capture failures never produced a frame, and a revoked session
// must not publish at all.
func deliverableRejection(code string) bool {
	switch code {
	case protocol.CodeTargetChanged, protocol.CodeForegroundChanged, protocol.CodeNodeNotFound:
		return true
	}
	return false
}

// markDelivered records that this operation published the rejected frame, so the
// single recovery planner can offer retry instead of observe.
func (e *Engine) markDelivered(op *operationState) {
	if op == nil {
		return
	}
	e.mu.Lock()
	op.Delivered = true
	e.mu.Unlock()
}

func treeFormat(public *protocol.Observation) string {
	if public == nil || public.Tree == nil {
		return ""
	}
	if public.Tree.Delta != nil {
		return "delta"
	}
	return "full"
}

// logDelivery records only the decision and the controlled code: never the
// observation identity, tree content, point or text.
func (e *Engine) logDelivery(work *workState, state, code, format string) {
	operationID := ""
	if work != nil {
		operationID = work.OperationID
	}
	e.log.Event(eventlog.Debug, "act.delivered",
		eventlog.Str("operation_id", operationID),
		eventlog.Str("state", state), eventlog.Str("code", code),
		eventlog.Str("format", format))
}
