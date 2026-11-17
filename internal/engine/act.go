package engine

import (
	"context"
	"errors"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Act performs one strict action branch against the current ready observation.
// Static rejection happens before claiming; after claiming the old observation
// is never returned to ready, even when no input was dispatched.
func (e *Engine) Act(ctx context.Context, agent, p string, req *protocol.ActRequest) (finalResult *protocol.Result, finalErr *protocol.APIError) {
	if req == nil {
		return nil, protocol.Invalid("act 请求缺失")
	}
	if apiErr := req.Operation.ValidateEffectiveDefaults(); apiErr != nil {
		return nil, apiErr
	}
	op, duplicate, apiErr := e.registerOperation(p, agent, "act")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		return duplicate, nil
	}
	defer func() {
		e.applyFrozenResult(op, &finalResult)
		e.applyLiveTextProjection(op, &finalResult)
		e.logTextFinished(op, finalResult)
	}()
	e.setOperationReference(op, protocol.RecoveryReferenceO)
	if req.Operation.Type == "text" {
		// Record the semantic action before the work position is taken so a
		// static rejection, busy exit or frozen P still knows it was text.
		e.recordActAction(op, "text")
	}

	e.mu.Lock()
	staticErr := e.staticActCheckLocked(agent, req.Reference, &req.Operation, time.Now())
	staticObs := e.observations[req.Reference]
	e.mu.Unlock()
	var point *pointCheck
	var scroll *scrollPlan
	var tap *tapPlan
	if staticErr == nil {
		point, scroll, tap, staticErr = staticActContentCheckWithTap(staticObs, &req.Operation)
	}
	if staticErr != nil {
		e.logActPoint(op.ID, point, false)
		if tap != nil {
			e.logTapPlan(op.ID, tap.action, tap.strategy, false, tap.took)
		}
		return e.finishWithError(op, "admission", staticErr), nil
	}
	var tapTook time.Duration
	tapProven := false
	if tap != nil {
		tapTook = tap.took
		defer func() {
			e.logTapPlan(op.ID, tap.action, tap.strategy, tapProven, tapTook)
		}()
	}
	if scroll != nil {
		e.logScrollPlan(op.ID, scroll)
		// The action fact must exist before the work position is taken: busy,
		// dispatch-unknown, revoked and post-failure exits must still let
		// status(P) know the original action.
		e.recordScrollAction(op)
	}

	work, busy := e.acquireWork(op, WorkAct)
	if busy != nil {
		return e.finishWithError(op, "admission", busy), nil
	}
	defer e.releaseWork(work)

	e.mu.Lock()
	session := e.session
	obs := e.observations[req.Reference]
	claimErr := e.claimObservationLocked(op, agent, work, obs, session, time.Now())
	if claimErr != nil {
		e.mu.Unlock()
		return e.finishWithError(op, "claim", claimErr), nil
	}
	if req.Operation.Type == "text" {
		op.TextStagePressEnter = req.Operation.EffectivePressEnter()
		e.initTextStageLocked(op, session)
	}
	e.touchSessionLocked(time.Now())
	e.mu.Unlock()

	e.setOperationPhase(op, "revalidate")
	var fresh *device.Capture
	var tapFrame *tapFrame
	var frame *scrollFrame
	var revalidateErr *protocol.APIError
	if scroll != nil {
		fresh, frame, revalidateErr = e.revalidateScrollTarget(work, obs, scroll)
	} else {
		var proofTook time.Duration
		fresh, tapFrame, proofTook, revalidateErr =
			e.revalidateActTarget(work, obs, &req.Operation, point, tap)
		tapTook += proofTook
	}
	if revalidateErr != nil {
		if e.workRevoked(work) || work.Ctx.Err() != nil {
			revalidateErr = stoppedActRevalidation()
		}
		if req.Operation.Type == "text" {
			e.decorateTextRevalidateError(op, revalidateErr)
		}
		e.logActPoint(op.ID, point, false)
		revalidateErr.Outcome = OutcomeNotExecuted
		// The rejected revalidation has already read one frame. Publishing it
		// hands the caller fresh evidence instead of making it observe again;
		// nothing was dispatched, so the recovery becomes retry against that O.
		var public *protocol.Observation
		if deliverableRejection(revalidateErr.Code) &&
			!e.workRevoked(work) && work.Ctx.Err() == nil {
			public = deliverRejection(e, work, session, obs, fresh, revalidateErr.Code)
		}
		if public != nil {
			e.markDelivered(op)
		}
		result := e.finishWithError(op, "revalidate", revalidateErr)
		if public != nil {
			result.Observation = public
		}
		return result, nil
	}
	tapProven = true
	e.logActPoint(op.ID, point, point != nil && point.state == pointUnique)

	e.setOperationPhase(op, "dispatch")
	var outcome string
	var dispatchErr *protocol.APIError
	if scroll != nil {
		outcome, dispatchErr = e.dispatchScroll(work, frame)
	} else {
		outcome, dispatchErr = e.dispatchAct(work, op, obs, &req.Operation, fresh, tapFrame)
	}
	if outcome == OutcomeUncertain {
		e.markDispatchUnknown(work)
		result := e.newResult(op)
		result.Outcome = OutcomeUncertain
		result.Error = dispatchErr.Public()
		if scroll != nil {
			// A dispatched scroll whose receipt is unconfirmed has no effect
			// fact: unknown is the honest projection, never not_observed.
			result.Effect = &protocol.Effect{State: protocol.EffectUnknown}
		}
		result.Recovery = e.finishOperation(op, OutcomeUncertain, "dispatch", dispatchErr)
		e.addSessionIfEnded(result)
		return result, nil
	}
	if outcome == OutcomeNotExecuted {
		dispatchErr.Outcome = OutcomeNotExecuted
		return e.finishWithError(op, "dispatch", dispatchErr), nil
	}

	e.clearDispatchUnknown(work)
	e.noteKnownOutcome(op, OutcomeExecuted)
	e.setOperationPhase(op, "post_observation")
	if e.workRevoked(work) {
		result := e.newResult(op)
		result.Outcome = OutcomeExecuted
		addScrollUnknownEffect(result, scroll)
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	session = e.sessionForWork(work)
	if session == nil {
		result := e.newResult(op)
		result.Outcome = OutcomeExecuted
		addScrollUnknownEffect(result, scroll)
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	public, postObs, postErr := e.captureAndPublish(work.Ctx, work, session, false, req.Image, "act")
	if postErr != nil && postErr.Code == protocol.CodeStopped {
		// A revoked session has no post observation to publish; the action
		// itself stays executed. This is the same fact as the pre-capture
		// revocation check above, only observed one step later.
		result := e.newResult(op)
		result.Outcome = OutcomeExecuted
		addScrollUnknownEffect(result, scroll)
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	result := e.newResult(op)
	result.Outcome = OutcomeExecuted
	if postErr != nil {
		// The action outcome stays executed; the post observation only reports
		// the failed tree, or read-only image material of that failed tree.
		if public != nil {
			result.Observation = public
		} else {
			result.Observation = &protocol.Observation{
				Status: "failed", Missing: []string{"tree"}, Error: postErr.Public(),
			}
		}
		addScrollUnknownEffect(result, scroll)
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "post_observation_failed", nil)
		return result, nil
	}
	result.Outcome = OutcomeExecuted
	result.Observation = public
	if scroll != nil {
		state := e.inferScrollEffect(work, frame, postObs, public, scroll.direction)
		if e.commitScrollEffect(work, op, state) {
			result.Effect = &protocol.Effect{State: state}
		} else {
			// The effect was computed outside the state lock but its commit
			// gate no longer holds (revoked/frozen): the first result must not
			// claim a fact the retained P cannot project.
			result.Effect = &protocol.Effect{State: protocol.EffectUnknown}
		}
	}
	result.Recovery = e.finishOperation(op, OutcomeExecuted, "finished", nil)
	return result, nil
}

// addScrollUnknownEffect projects the bounded unknown effect of a scroll
// attempt that ended before any effect could be computed.
func addScrollUnknownEffect(result *protocol.Result, plan *scrollPlan) {
	if result != nil && plan != nil {
		result.Effect = &protocol.Effect{State: protocol.EffectUnknown}
	}
}

func (e *Engine) addSessionIfEnded(result *protocol.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	op := e.operations[result.OperationID]
	if op == nil {
		return
	}
	if session := e.sessionByOriginLocked(op.SessionOrigin); session != nil {
		result.IncludeSession = true
		result.Session = e.sessionViewLocked(session)
	}
}

// staticActCheckLocked validates the operation against the immutable observation
// content. It performs no claim and no device I/O.
func (e *Engine) staticActCheckLocked(agent, observationID string, operation *protocol.Operation, now time.Time) *protocol.APIError {
	session := e.session
	if session == nil || session.State != SessionActive {
		return protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	if !sessionBeforeDeadline(session, now) {
		return protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期")
	}
	obs := e.observations[observationID]
	if obs == nil || obs.Agent != agent || obs.SessionEpoch != session.Epoch {
		return protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
	}
	if e.current != obs {
		return protocol.NewError(409, protocol.CodeObservationRetired, "观察已被替换")
	}
	switch obs.Status {
	case ObservationReady:
	case ObservationClaimed:
		return protocol.NewError(409, protocol.CodeObservationClaimed, "观察已被消费")
	case ObservationExpired:
		return protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期")
	default:
		return protocol.NewError(409, protocol.CodeObservationRetired, "观察已退役")
	}
	if now.After(obs.ActionableUntil) {
		obs.Status = ObservationExpired
		return protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期")
	}
	return nil
}

// The projection is immutable; all node traversal happens outside e.mu.
func staticActContentCheck(obs *observationState, operation *protocol.Operation) (*pointCheck, *scrollPlan, *protocol.APIError) {
	screen := obs.Screen
	switch operation.Type {
	case "tap", "long_press", "text":
		if operation.Target == nil {
			return nil, nil, protocol.Invalid("操作缺少 target")
		}
		if operation.Target.UseRef {
			node := findPublicNode(obs.Projection.Tree.Root, operation.Target.Ref)
			if node == nil {
				return nil, nil, protocol.NewError(409, protocol.CodeNodeNotFound, "目标节点不存在")
			}
			if node.Role == protocol.RoleRoot || node.Context || node.Bounds == nil {
				return nil, nil, protocol.NewError(422, protocol.CodeNodeUnsupported, "该节点不能作为操作目标")
			}
			if node.Enabled != nil && !*node.Enabled {
				return nil, nil, protocol.NewError(409, protocol.CodeNodeDisabled, "目标节点已禁用")
			}
			switch operation.Type {
			case "tap":
				if !hasAction(node, protocol.ActionTap) {
					return nil, nil, protocol.NewError(422, protocol.CodeNodeUnsupported, "目标节点不支持点击")
				}
			case "long_press":
				if !hasAction(node, protocol.ActionLongPress) {
					return nil, nil, protocol.NewError(422, protocol.CodeNodeUnsupported, "目标节点不支持长按")
				}
			case "text":
				if node.Role != protocol.RoleInput || !hasAction(node, protocol.ActionText) {
					return nil, nil, staticTextError(textReasonTargetNotEditable)
				}
				if node.Protected == nil || *node.Protected {
					return nil, nil, staticTextError(textReasonTargetProtected)
				}
			}
			return nil, nil, nil
		}
		if !inScreen(operation.Target.X, operation.Target.Y, screen) {
			return nil, nil, protocol.NewError(422, protocol.CodeInvalidCoordinates, "坐标超出目标屏幕")
		}
		check, apiErr := resolveObservedPoint(obs, operation)
		return check, nil, apiErr
	case "swipe":
		if operation.From == nil || operation.To == nil {
			return nil, nil, protocol.Invalid("swipe 缺少起终点")
		}
		if operation.Target != nil || operation.Container != nil || operation.Key != "" ||
			operation.Text != "" || operation.Direction != "" || operation.Amount != "" {
			return nil, nil, protocol.Invalid("swipe 不接受 target/container/key/text/direction/amount")
		}
		if !inScreen(operation.From.X, operation.From.Y, screen) ||
			!inScreen(operation.To.X, operation.To.Y, screen) {
			return nil, nil, protocol.NewError(422, protocol.CodeInvalidCoordinates, "坐标超出目标屏幕")
		}
		check, apiErr := resolveObservedPoint(obs, operation)
		return check, nil, apiErr
	case "scroll":
		plan, apiErr := resolveScrollPlan(obs, operation)
		return nil, plan, apiErr
	case "key":
		if screen.Rotation < 0 {
			return nil, nil, protocol.Invalid("屏幕信息无效")
		}
		if screen.ID != 0 && (operation.Key == "HOME" || operation.Key == "APP_SWITCH") {
			return nil, nil, protocol.NewError(422, protocol.CodeInvalidKey,
				"HOME/APP_SWITCH 需要共享主屏")
		}
	}
	return nil, nil, nil
}

// staticActContentCheckWithTap keeps the existing coordinate, text, swipe,
// scroll and key branches unchanged while giving tap/long_press ref targets
// their own bounded subtree planner.
func staticActContentCheckWithTap(obs *observationState, operation *protocol.Operation) (
	*pointCheck, *scrollPlan, *tapPlan, *protocol.APIError) {
	if operation != nil && (operation.Type == "tap" || operation.Type == "long_press") &&
		operation.Target != nil && operation.Target.UseRef {
		return staticTapRefCheck(obs, operation)
	}
	point, scroll, apiErr := staticActContentCheck(obs, operation)
	if apiErr == nil && operation != nil && operation.Type == "text" &&
		operation.Target != nil && operation.Target.UseRef {
		_, _, plan, planErr := staticTextRefCheck(obs, operation)
		return point, scroll, plan, planErr
	}
	return point, scroll, nil, apiErr
}

// claimObservationLocked performs the one-way ready->claimed transition and
// processes the reading acknowledgement of a full candidate.
func (e *Engine) claimObservationLocked(op *operationState, agent string, work *workState, obs *observationState,
	session *sessionState, now time.Time) *protocol.APIError {
	if session == nil || session.State != SessionActive || e.session != session {
		return protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	if !e.workAuthorizedLocked(work, now) {
		return protocol.NewError(409, protocol.CodeStopped, "原会话已撤销")
	}
	if obs == nil || obs.Agent != agent || obs.SessionEpoch != session.Epoch {
		return protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
	}
	if e.current != obs || obs.Status != ObservationReady {
		return protocol.NewError(409, protocol.CodeObservationRetired, "观察已被替换")
	}
	if now.After(obs.ActionableUntil) {
		obs.Status = ObservationExpired
		return protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期")
	}
	obs.Status = ObservationClaimed
	obs.ClaimedBy = work.OperationID
	work.ClaimedO = obs.ID
	if op != nil {
		op.Claimed = true
	}
	if obs.Full {
		e.confirmBaselineLocked(obs)
	}
	return nil
}

// captureForActRevalidation is the common ProbeDisplay/Capture prefix. A STOP
// that lands while a device call is in flight wins over the device error, and
// a frame obtained after revocation is never handed back for delivery.
func (e *Engine) captureForActRevalidation(work *workState) (
	*device.Capture, *sessionState, *protocol.APIError) {
	if work == nil || work.Ctx == nil || e.workRevoked(work) ||
		work.Ctx.Err() != nil {
		return nil, nil, stoppedActRevalidation()
	}
	session := e.sessionForWork(work)
	if session == nil {
		return nil, nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	probeCtx, cancel := context.WithTimeout(work.Ctx, 5*time.Second)
	state, err := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, stoppedActRevalidation()
	}
	if err != nil || !state.Exists {
		return nil, nil, protocol.NewError(503, protocol.CodeDisplayUnavailable,
			"目标屏幕不可用")
	}
	capture, err := e.dev.Capture(work.Ctx, session.Display)
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, stoppedActRevalidation()
	}
	if err != nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed,
			"实时目标重验失败")
	}
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return nil, nil, stoppedActRevalidation()
	}
	return &capture, session, nil
}

func stoppedActRevalidation() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
}

// probeActNode checks STOP before the matcher, at every Tier boundary inside
// it, and once more before its unique result may reach a caller.
func (e *Engine) probeActNode(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode) (actMatch, actProbeState) {
	if ctx == nil || ctx.Err() != nil || (revoked != nil && revoked()) {
		return actMatch{}, actProbeCancelled
	}
	match, state := matchActTarget(ctx, revoked, original, fresh, want)
	if ctx.Err() != nil || (revoked != nil && revoked()) {
		return actMatch{}, actProbeCancelled
	}
	return match, state
}

// revalidateActTarget re-reads the live display and maps a requested target to
// the fresh frame. Coordinate targets keep the original point proof. Ref tap,
// long_press and text targets go through the Tier 1-3 matcher and receive an
// immutable frame carrying the actual point and carrier used by dispatch. A
// content rejection returns the fresh capture already read so the caller may
// deliver it as evidence, unless STOP has since revoked the work.
func (e *Engine) revalidateActTarget(work *workState, obs *observationState,
	operation *protocol.Operation, point *pointCheck, tap *tapPlan) (
	*device.Capture, *tapFrame, time.Duration, *protocol.APIError) {
	capture, _, apiErr := e.captureForActRevalidation(work)
	if apiErr != nil {
		return nil, nil, 0, apiErr
	}
	if apiErr := foregroundMatches(obs.Foreground, capture.Foreground); apiErr != nil {
		return capture, nil, 0, apiErr
	}
	if point != nil && point.state == pointUnique {
		if apiErr := e.provePointTarget(capture, obs, point); apiErr != nil {
			return capture, nil, 0, apiErr
		}
	}
	if operation.Target == nil || !operation.Target.UseRef {
		return capture, nil, 0, nil
	}
	if tap == nil {
		return capture, nil, 0, tapHierarchyError()
	}
	started := time.Now()
	revoked := func() bool { return e.workRevoked(work) }
	frame, apiErr := e.revalidateTapTargetFrame(
		work.Ctx, revoked, work.Projector, obs, capture, tap)
	took := time.Since(started)
	if apiErr != nil {
		return capture, nil, took, apiErr
	}
	if operation.Type == "text" {
		if frame == nil || !freshTextTargetSafe(frame.raw) {
			return capture, nil, took, tapTargetChanged()
		}
	}
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return capture, nil, took, stoppedActRevalidation()
	}
	return capture, frame, took, nil
}

func freshTextTargetSafe(raw *device.RawNode) bool {
	return raw != nil && raw.Visible && raw.Editable &&
		raw.HasEnabled && raw.Enabled &&
		raw.HasPassword && !raw.Password
}

// revalidateBubbleTapFrame re-proves both identities of a C->A target lift on
// the fresh frame. The child stays the authorization selector: if it is lost,
// even a uniquely matched ancestor cannot silently inherit the action.
func (e *Engine) revalidateBubbleTapFrame(ctx context.Context,
	revoked func() bool, workProjector Projector, obs *observationState,
	fresh *device.Capture, plan *tapPlan) (*tapFrame, *protocol.APIError) {
	stop := func() bool {
		return ctx == nil || ctx.Err() != nil || (revoked != nil && revoked())
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	topology, apiErr := validateTapRawTopology(fresh)
	if apiErr != nil {
		return nil, apiErr
	}
	matchChild, childState := e.probeActNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.targetRaw)
	switch childState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, tapTargetChanged()
	default:
		return nil, tapHierarchyError()
	}
	matchAncestor, ancestorState := e.probeActNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.bubbleAncestorRaw)
	switch ancestorState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, tapTargetChanged()
	default:
		return nil, tapHierarchyError()
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	projector := workProjector
	if projector == nil {
		projector = e.projector
	}
	if projector == nil {
		return nil, tapHierarchyError()
	}
	projection, err := projector.Preview(fresh)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if err != nil || projection == nil || projection.Tree == nil ||
		projection.Tree.Root == nil || projection.Source == nil ||
		projection.Capture == nil {
		return nil, tapHierarchyError()
	}
	view, apiErr := buildTapPublicView(projection.Tree, fresh, projection.Source)
	if apiErr != nil {
		return nil, apiErr
	}
	childIndex := indexOf(fresh, matchChild.raw)
	ancestorIndex := indexOf(fresh, matchAncestor.raw)
	if childIndex < 0 || ancestorIndex < 0 {
		return nil, tapHierarchyError()
	}
	switch checkTapSourceMapping(view, topology, childIndex) {
	case tapSourceTargetMissing:
		return nil, tapTargetChanged()
	case tapSourceDamaged:
		return nil, tapHierarchyError()
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	childPublic := view.byRaw[childIndex]
	ancestorPublic := view.byRaw[ancestorIndex]
	if childPublic == nil || ancestorPublic == nil || view.byRaw[ancestorIndex] != ancestorPublic {
		return nil, tapTargetChanged()
	}
	originalPublic := findPublicNode(obs.Projection.Tree.Root, plan.targetRef)
	if originalPublic == nil {
		return nil, tapTargetChanged()
	}
	if childPublic.Role != originalPublic.Role ||
		!samePublicLabel(childPublic.Label, originalPublic.Label) {
		return nil, tapTargetChanged()
	}
	// The child must still be non-interactive. Any new tap/long_press/text
	// capability, including the requested one, invalidates the lift plan.
	if isTouchInteractive(childPublic) {
		return nil, tapTargetChanged()
	}
	if !validTapPublicTarget(ancestorPublic, protocol.Action(plan.action)) {
		return nil, tapTargetChanged()
	}
	if !topology.isAncestor(ancestorIndex, childIndex) {
		return nil, tapTargetChanged()
	}
	if nearestInteractiveAncestor(view, childPublic) != ancestorPublic {
		return nil, tapTargetChanged()
	}
	ancestorInput := &tapInput{
		view: view, capture: fresh, topology: topology,
		target: ancestorPublic, targetRaw: matchAncestor.raw, targetIndex: ancestorIndex,
	}
	scope, apiErr := collectTapScope(ancestorInput)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if apiErr != nil {
		if apiErr.Code == protocol.CodeHierarchyFailed {
			return nil, apiErr
		}
		return nil, tapTargetChanged()
	}
	x, y, strategy, carrier, ok := chooseBubbleTapPoint(scope, view, ancestorPublic, childPublic)
	if !ok {
		return nil, tapTargetChanged()
	}
	frame := &tapFrame{
		capture: fresh, raw: matchAncestor.raw, x: x, y: y,
		tier: matchAncestor.tier, strategy: strategy,
	}
	if carrier != nil {
		frame.carrier = carrier.raw
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	return frame, nil
}

// revalidateTapTargetFrame proves one fresh ref target and computes the exact
// point dispatch is allowed to use. The returned frame is immutable: target,
// carrier, coordinates, Tier and strategy are fixed before the first primitive.
func (e *Engine) revalidateTapTargetFrame(ctx context.Context,
	revoked func() bool, workProjector Projector, obs *observationState,
	fresh *device.Capture, plan *tapPlan) (*tapFrame, *protocol.APIError) {
	stop := func() bool {
		return ctx == nil || ctx.Err() != nil || (revoked != nil && revoked())
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if ctx == nil || fresh == nil || plan == nil || plan.targetRaw == nil ||
		obs == nil || obs.Projection == nil || obs.Projection.Capture == nil {
		return nil, tapHierarchyError()
	}
	if plan.bubbleAncestorRaw != nil {
		return e.revalidateBubbleTapFrame(ctx, revoked, workProjector, obs, fresh, plan)
	}
	topology, apiErr := validateTapRawTopology(fresh)
	if apiErr != nil {
		return nil, apiErr
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}

	match, matchState := e.probeActNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.targetRaw)
	switch matchState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, tapTargetChanged()
	default:
		return nil, tapHierarchyError()
	}
	targetRaw := match.raw

	projector := workProjector
	if projector == nil {
		projector = e.projector
	}
	if projector == nil {
		return nil, tapHierarchyError()
	}
	projection, err := projector.Preview(fresh)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if err != nil || projection == nil || projection.Tree == nil ||
		projection.Tree.Root == nil || projection.Source == nil ||
		projection.Capture == nil {
		return nil, tapHierarchyError()
	}
	view, apiErr := buildTapPublicView(projection.Tree, fresh, projection.Source)
	if apiErr != nil {
		return nil, apiErr
	}
	targetIndex := indexOf(fresh, targetRaw)
	if targetIndex < 0 {
		return nil, tapHierarchyError()
	}
	switch checkTapSourceMapping(view, topology, targetIndex) {
	case tapSourceTargetMissing:
		return nil, tapTargetChanged()
	case tapSourceDamaged:
		return nil, tapHierarchyError()
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	targetPublic := view.byRaw[targetIndex]
	if !validTapPublicTarget(targetPublic, protocol.Action(plan.action)) {
		return nil, tapTargetChanged()
	}
	input := &tapInput{
		view: view, capture: fresh, topology: topology,
		target: targetPublic, targetRaw: targetRaw, targetIndex: targetIndex,
	}
	scope, apiErr := collectTapScope(input)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if apiErr != nil {
		if apiErr.Code == protocol.CodeHierarchyFailed {
			return nil, apiErr
		}
		return nil, tapTargetChanged()
	}

	originalPointCarrier, originalPointOK := pointOwner(scope, plan.x, plan.y)

	var fixedCarrier *tapCarrier
	if plan.carrierRaw != nil {
		carrierMatch, carrierState := e.probeActNode(
			ctx, revoked, obs.Projection.Capture, fresh, plan.carrierRaw)
		switch carrierState {
		case actProbeUnique:
			carrierIndex := indexOf(fresh, carrierMatch.raw)
			if carrierIndex < 0 {
				return nil, tapHierarchyError()
			}
			if carrierIndex != targetIndex &&
				topology.isAncestor(targetIndex, carrierIndex) {
				if carrierPublic := view.byRaw[carrierIndex]; carrierPublic != nil &&
					!carrierPublic.Context &&
					carrierPublic.Role != protocol.RoleRoot &&
					carrierPublic.Bounds != nil {
					fixedCarrier = scope.carrierByRef[carrierPublic.Ref]
				}
			}
		case actProbeCancelled:
			return nil, stoppedActRevalidation()
		case actProbeBudgetExhausted, actProbeMalformed:
			return nil, tapHierarchyError()
		case actProbeNone, actProbeAmbiguous:
			// A lost or ambiguous carrier may be unbound only after the
			// independent target-content gate below.
		default:
			return nil, tapHierarchyError()
		}
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}

	if originalPointOK &&
		(plan.carrierRaw == nil || fixedCarrier != nil || match.independentContent) {
		if stop() {
			return nil, stoppedActRevalidation()
		}
		return &tapFrame{
			capture: fresh, raw: targetRaw, x: plan.x, y: plan.y,
			carrier: carrierRaw(originalPointCarrier), tier: match.tier,
			strategy: inputPlanStrategyForPoint(scope, originalPointCarrier),
		}, nil
	}

	if fixedCarrier != nil {
		if x, y, strategy, ok := fixedCarrierSafePoint(
			scope, targetRaw, fixedCarrier); ok {
			if stop() {
				return nil, stoppedActRevalidation()
			}
			return &tapFrame{
				capture: fresh, raw: targetRaw, x: x, y: y,
				carrier: fixedCarrier.raw, tier: match.tier, strategy: strategy,
			}, nil
		}
	}

	if plan.carrierRaw != nil && !match.independentContent {
		return nil, tapTargetChanged()
	}
	unbound, apiErr := chooseUnboundTapPlan(
		scope, input, targetPublic, protocol.Action(plan.action), plan.x, plan.y)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if apiErr != nil || unbound == nil || unbound.strategy == tapModeRejected {
		return nil, tapTargetChanged()
	}
	if !provesSubtreeTapPoint(scope, unbound.x, unbound.y) {
		return nil, tapTargetChanged()
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	return &tapFrame{
		capture: fresh, raw: targetRaw, x: unbound.x, y: unbound.y,
		carrier: unbound.carrierRaw, tier: match.tier, strategy: unbound.strategy,
	}, nil
}

// provePointTarget requires the observed node to match exactly one fresh node
// by complete content identity and ancestry, and then requires the fresh public
// tree to resolve the same point to that same source node. Any other result is
// a rejection: ambiguity, a missing node and a different node are all "the
// meaning of this point changed". The projection is pure CPU and never
// advances ref continuity.
func (e *Engine) provePointTarget(fresh *device.Capture, obs *observationState,
	point *pointCheck) *protocol.APIError {
	if point.index < 0 || point.index >= len(obs.Projection.Capture.Nodes) {
		return targetChangedAtPoint()
	}
	original := &obs.Projection.Capture.Nodes[point.index]
	matched, ok := matchFreshNode(fresh, obs.Projection.Capture, original)
	if !ok {
		return targetChangedAtPoint()
	}
	started := time.Now()
	// The proof maps a display point to a fresh raw node; it needs a projection
	// with a correct source map, never the session's ref continuity.
	projection, err := e.projector.Preview(fresh)
	if err != nil {
		point.took += time.Since(started)
		return protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	ref, state := resolvePoint(projection.Tree.Root, point.x, point.y)
	freshIndex, mapped := projection.Source[ref]
	matchedIndex := indexOf(fresh, matched)
	point.took += time.Since(started)
	if state != pointUnique || !mapped || freshIndex != matchedIndex {
		return targetChangedAtPoint()
	}
	return nil
}

func targetChangedAtPoint() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeTargetChanged,
		"目标点上的节点已变化，本次未派发")
}

// logActPoint records one coordinate-target decision. Only the operation, the
// resolution state, whether the node-level proof succeeded and the pure-CPU
// cost are written; the point, refs, labels and tree content never are.
func (e *Engine) logActPoint(operationID string, point *pointCheck, proven bool) {
	if point == nil {
		return
	}
	e.log.Event(eventlog.Debug, "act.point",
		eventlog.Str("operation_id", operationID),
		eventlog.Str("state", string(point.state)),
		eventlog.Bool("proven", proven),
		eventlog.Duration("ms", point.took))
}

// logTapPlan records one subtree-plan decision. The coordinates, refs, labels
// and tree content are deliberately absent.
func (e *Engine) logTapPlan(operationID, action, mode string, proven bool, took time.Duration) {
	if mode == "" {
		return
	}
	e.log.Event(eventlog.Debug, "act.tap_plan",
		eventlog.Str("operation_id", operationID),
		eventlog.Str("action", action),
		eventlog.Str("mode", mode),
		eventlog.Bool("proven", proven),
		eventlog.Duration("ms", took))
}

func foregroundMatches(observed, fresh device.Foreground) *protocol.APIError {
	switch observed.State {
	case device.ForegroundPresent:
		if fresh.State != device.ForegroundPresent || fresh.Package != observed.Package {
			return protocol.NewError(409, protocol.CodeForegroundChanged,
				"前台窗口已变化，本次未派发")
		}
		if observed.TaskID != nil && fresh.TaskID != nil && *observed.TaskID != *fresh.TaskID {
			return protocol.NewError(409, protocol.CodeForegroundChanged,
				"前台任务已变化，本次未派发")
		}
		return nil
	case device.ForegroundNone:
		if fresh.State != device.ForegroundNone {
			return protocol.NewError(409, protocol.CodeForegroundChanged,
				"前台窗口已变化，本次未派发")
		}
		return nil
	default:
		return protocol.NewError(409, protocol.CodeForegroundUnknown,
			"无法确认目标窗口归属，本次未派发")
	}
}

// dispatchAct executes exactly one input branch and reports whether the device
// accepted it. Anything after the first dispatched primitive that cannot be
// confirmed is uncertain; nothing is replayed.
func (e *Engine) dispatchAct(work *workState, op *operationState, obs *observationState,
	operation *protocol.Operation, fresh *device.Capture,
	frame *tapFrame) (string, *protocol.APIError) {
	session := e.sessionForWork(work)
	if session == nil || e.workRevoked(work) {
		return OutcomeNotExecuted, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	displayID := session.Display.ID
	if operation.Type == "text" {
		return e.dispatchText(work, op, session, obs, operation, fresh, frame)
	}
	x, y := 0, 0
	if operation.Target != nil {
		x, y = operation.Target.X, operation.Target.Y
	}
	if operation.Target != nil && operation.Target.UseRef {
		if frame == nil || (operation.Type == "tap" || operation.Type == "long_press") == false {
			return OutcomeNotExecuted, tapTargetChanged()
		}
		x, y = frame.x, frame.y
	}
	if apiErr := e.beginUIDispatch(work, operation.Type); apiErr != nil {
		return OutcomeNotExecuted, apiErr
	}
	var receipt device.DispatchReceipt
	var dispatchErr error
	switch operation.Type {
	case "key":
		receipt, dispatchErr = e.dev.Key(work.Ctx, displayID, operation.Key)
	case "tap":
		receipt, dispatchErr = e.dev.Tap(work.Ctx, displayID, x, y)
	case "long_press":
		receipt, dispatchErr = e.dev.LongPress(work.Ctx, displayID, x, y)
	case "swipe":
		duration := operation.DurationMS
		if duration == 0 {
			// An engine-internal construction may omit the decoded default;
			// the adapter requires an explicit bounded duration.
			duration = protocol.DefaultSwipeDurationMS
		}
		receipt, dispatchErr = e.dev.Swipe(work.Ctx, displayID, operation.From.X, operation.From.Y,
			operation.To.X, operation.To.Y, duration)
	default:
		receipt = device.DispatchNotDispatched
		dispatchErr = protocol.Invalid("不支持的操作")
	}
	e.recordUIReceipt(work, receipt)
	if receipt == device.DispatchNotDispatched {
		return OutcomeNotExecuted, protocol.NewError(503, protocol.CodeDeviceUnavailable, "输入未派发")
	}
	if dispatchErr != nil || receipt != device.DispatchTerminated {
		return OutcomeUncertain, actionUncertain()
	}
	if !e.confirmDispatch(work) {
		return OutcomeUncertain, actionUncertain()
	}
	return OutcomeExecuted, nil
}

// dispatchText performs at most one focus tap, one display-bound set and one
// independent ENTER. A ref that is already focused in the fresh frame and
// passes the complete binding gate skips the focus tap. The set call's own
// refreshed value of the same node is the write completion evidence; no second
// tree acquisition or GetText is used to re-prove the write. The set is never
// replayed and text is never rolled back.
func (e *Engine) dispatchText(work *workState, op *operationState, session *sessionState,
	obs *observationState, operation *protocol.Operation,
	fresh *device.Capture, frame *tapFrame) (string, *protocol.APIError) {
	if operation == nil || operation.Target == nil || fresh == nil {
		return OutcomeNotExecuted, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"无法唯一绑定目标输入对象", textStageTarget, textReasonTargetUnconfirmed,
			OutcomeNotExecuted, nil)
	}
	composite := operation.EffectivePressEnter()

	var bound textBinding
	var apiErr *protocol.APIError
	if operation.Target.UseRef {
		if frame == nil || obs == nil || obs.Projection == nil || obs.Projection.Capture == nil {
			return OutcomeNotExecuted, e.textError(op, 409, protocol.CodeTextTargetUnknown,
				"无法唯一绑定目标输入对象", textStageTarget, textReasonTargetUnconfirmed,
				OutcomeNotExecuted, nil)
		}
		tapX, tapY := frame.x, frame.y
		want := frame.raw
		// The no-tap fast path is allowed only for a ref that is already known
		// focused and safe in the fresh frame, and only through the complete
		// binder: anchor uniqueness, TaskID and source presence are unchanged.
		if want != nil && want.Visible && want.Editable && want.HasFocused && want.Focused &&
			want.HasEnabled && want.Enabled && want.HasPassword && !want.Password {
			var ok bool
			bound, ok = bindFocusedText(obs.Projection.Capture, fresh, want, obs.Foreground, tapX, tapY)
			if !ok {
				bound = textBinding{}
			}
		}
		if bound.raw == nil {
			bound, apiErr = e.focusTextRef(work, op, session, obs, fresh, want, tapX, tapY)
			if apiErr != nil {
				return apiErr.Outcome, apiErr
			}
		}
	} else {
		tapX, tapY := operation.Target.X, operation.Target.Y
		bound, apiErr = e.focusTextPoint(work, op, session, fresh, tapX, tapY)
		if apiErr != nil {
			return apiErr.Outcome, apiErr
		}
	}
	target := textTargetOf(bound.raw, session.Display)

	if !e.setTextStage(work, op, textStageWrite) {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", e.textCurrentStage(op), textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	if apiErr := e.beginUIDispatchStage(work, "text", "write"); apiErr != nil {
		if apiErr.Code == protocol.CodeStopped {
			return e.textFailureOutcome(op), e.textError(op, 409, protocol.CodeStopped,
				"输入未派发：原工作已撤销", textStageWrite, textReasonAuthorizationRevoked,
				e.textFailureOutcome(op), nil)
		}
		return e.textFailureOutcome(op), e.textError(op, 503, protocol.CodeDeviceUnavailable,
			"输入未派发", textStageWrite, textReasonDispatchNotSent,
			e.textFailureOutcome(op), nil)
	}
	result, err := e.dev.SetTextOnDisplay(work.Ctx, target, operation.Text)
	e.recordUIReceipt(work, result.Receipt)
	if result.Receipt == device.DispatchNotDispatched {
		e.commitTextReceipt(work, op, "write", result.Receipt, err)
		return e.textFailureOutcome(op), e.textError(op, 503, protocol.CodeDeviceUnavailable,
			"输入未派发", textStageWrite, textReasonDispatchNotSent,
			e.textFailureOutcome(op), nil)
	}
	if result.Receipt != device.DispatchTerminated {
		e.commitTextReceipt(work, op, "write", result.Receipt, err)
		reason := textReasonReceiptUnknown
		if err != nil && errors.Is(err, device.ErrTextResultInvalid) {
			reason = textReasonRPCResultInvalid
		}
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"文字写入结果无法确认", textStageWrite, reason, OutcomeUncertain, nil)
	}
	e.commitTextReceipt(work, op, "write", device.DispatchTerminated, err)
	if err != nil {
		reason := textReasonReceiptUnknown
		if errors.Is(err, device.ErrTextResultInvalid) {
			reason = textReasonRPCResultInvalid
		}
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"文字写入结果无法确认", textStageWrite, reason, OutcomeUncertain, nil)
	}
	if result.CandidateCount != 1 {
		// A complete response with zero or multiple candidates proves the fixed
		// helper never called the write primitive for this target. A response
		// that nevertheless claims issuance or a value is contradictory and
		// stays an unknown receipt.
		if result.Issued || result.Accepted || result.Value != nil {
			return OutcomeUncertain, e.textError(op, 409, protocol.CodeTextResultUnknown,
				"文字写入结果无法确认", textStageWrite, textReasonRPCResultInvalid,
				OutcomeUncertain, nil)
		}
		e.setTextWriteNotDispatched(work, op)
		count := result.CandidateCount
		return e.textFailureOutcome(op), e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"目标输入对象无法唯一确认", textStageWrite, textReasonNoUniqueTarget,
			e.textFailureOutcome(op), &count)
	}
	if !result.Issued {
		// The decode layer rejects this contradiction; keep the same unknown
		// receipt classification if a different adapter ever produces it.
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"文字写入结果无法确认", textStageWrite, textReasonRPCResultInvalid,
			OutcomeUncertain, nil)
	}
	if !e.noteTextWriteDispatched(work, op) {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", e.textCurrentStage(op), textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	count := 1
	if result.Value == nil {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"文字写入结果无法确认", textStageVerify, textReasonReadbackUnavailable,
			OutcomeUncertain, &count)
	}
	if *result.Value != operation.Text {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"目标输入框的完整内容与请求不一致", textStageVerify, textReasonReadbackMismatch,
			OutcomeUncertain, &count)
	}
	if !e.commitTextWrite(work, op) {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", e.textCurrentStage(op), textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	if !composite {
		return OutcomeExecuted, nil
	}
	return e.dispatchTextEnter(work, op, session, obs, target, operation.Text)
}

// focusTextRef performs the one allowed focus tap for a ref target and binds
// the focused field in the after-tap frame under the current strict rules.
func (e *Engine) focusTextRef(work *workState, op *operationState, session *sessionState,
	obs *observationState, fresh *device.Capture, want *device.RawNode,
	tapX, tapY int) (textBinding, *protocol.APIError) {
	if !e.setTextStage(work, op, textStageFocus) {
		return textBinding{}, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageFocus, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	if apiErr := e.beginUIDispatchStage(work, "tap", "focus"); apiErr != nil {
		return textBinding{}, e.focusTextBeginError(op, apiErr)
	}
	receipt, tapErr := e.dev.Tap(work.Ctx, session.Display.ID, tapX, tapY)
	e.recordUIReceipt(work, receipt)
	e.commitTextReceipt(work, op, "focus", receipt, tapErr)
	if receipt == device.DispatchNotDispatched {
		return textBinding{}, e.textError(op, 503, protocol.CodeDeviceUnavailable,
			"输入未派发", textStageFocus, textReasonFocusNotSent,
			e.textFailureOutcome(op), nil)
	}
	if tapErr != nil || receipt != device.DispatchTerminated {
		return textBinding{}, e.textError(op, 504, protocol.CodeActionUnknown,
			"输入已派发，但完成结果无法确认", textStageFocus, textReasonFocusUnconfirmed,
			OutcomeUncertain, nil)
	}
	if e.workRevoked(work) {
		return textBinding{}, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageFocus, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	afterTap, err := e.dev.Capture(work.Ctx, session.Display)
	if err != nil {
		return textBinding{}, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"无法唯一绑定目标输入对象", textStageFocus, textReasonFocusUnconfirmed,
			OutcomeUncertain, nil)
	}
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return textBinding{}, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageFocus, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	bound, ok := bindFocusedText(fresh, &afterTap, want, fresh.Foreground, tapX, tapY)
	if !ok {
		return textBinding{}, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"无法唯一绑定目标输入对象", textStageFocus, textReasonFocusUnconfirmed,
			OutcomeUncertain, nil)
	}
	return bound, nil
}

// focusTextPoint preserves the coordinate-text path: the authorized point is
// focused once and only a unique safe focused field covering it can bind.
func (e *Engine) focusTextPoint(work *workState, op *operationState, session *sessionState,
	fresh *device.Capture, tapX, tapY int) (textBinding, *protocol.APIError) {
	if !e.setTextStage(work, op, textStageFocus) {
		return textBinding{}, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageFocus, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	if apiErr := e.beginUIDispatchStage(work, "tap", "focus"); apiErr != nil {
		return textBinding{}, e.focusTextBeginError(op, apiErr)
	}
	receipt, tapErr := e.dev.Tap(work.Ctx, session.Display.ID, tapX, tapY)
	e.recordUIReceipt(work, receipt)
	e.commitTextReceipt(work, op, "focus", receipt, tapErr)
	if receipt == device.DispatchNotDispatched {
		return textBinding{}, e.textError(op, 503, protocol.CodeDeviceUnavailable,
			"输入未派发", textStageFocus, textReasonFocusNotSent,
			e.textFailureOutcome(op), nil)
	}
	if tapErr != nil || receipt != device.DispatchTerminated {
		return textBinding{}, e.textError(op, 504, protocol.CodeActionUnknown,
			"输入已派发，但完成结果无法确认", textStageFocus, textReasonFocusUnconfirmed,
			OutcomeUncertain, nil)
	}
	if e.workRevoked(work) {
		return textBinding{}, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageFocus, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	afterTap, err := e.dev.Capture(work.Ctx, session.Display)
	if err != nil {
		return textBinding{}, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"无法唯一绑定目标输入对象", textStageFocus, textReasonFocusUnconfirmed,
			OutcomeUncertain, nil)
	}
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return textBinding{}, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageFocus, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	raw, ok := bindFocusedTextAtPoint(&afterTap, nil, tapX, tapY)
	if !ok {
		return textBinding{}, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"无法唯一绑定目标输入对象", textStageFocus, textReasonFocusUnconfirmed,
			OutcomeUncertain, nil)
	}
	return textBinding{raw: raw, mode: textNear}, nil
}

func (e *Engine) focusTextBeginError(op *operationState, apiErr *protocol.APIError) *protocol.APIError {
	if apiErr.Code == protocol.CodeStopped {
		return e.textError(op, 409, protocol.CodeStopped,
			"输入未派发：原工作已撤销", textStageFocus, textReasonAuthorizationRevoked,
			e.textFailureOutcome(op), nil)
	}
	return e.textError(op, 503, protocol.CodeDeviceUnavailable,
		"输入未派发", textStageFocus, textReasonFocusNotSent,
		e.textFailureOutcome(op), nil)
}

// dispatchTextEnter performs the independent ENTER boundary. The write is
// already confirmed; one read-only foreground check and one fresh exact read of
// the same target decide whether ENTER may be authorized. It is sent at most
// once and is never replayed.
func (e *Engine) dispatchTextEnter(work *workState, op *operationState, session *sessionState,
	obs *observationState, target device.TextTarget, text string) (string, *protocol.APIError) {
	if !e.setTextStage(work, op, textStageEnter) {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageEnter, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	if e.workRevoked(work) {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageEnter, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	foreground, err := e.dev.Foreground(work.Ctx, session.Display.ID)
	if err != nil || !sameTextTaskContext(obs.Foreground, foreground) {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"无法确认 ENTER 的目标上下文", textStageEnter,
			textReasonEnterContextUnconfirmed, OutcomeUncertain, nil)
	}
	if e.workRevoked(work) {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageEnter, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	read, readErr := e.dev.GetTextOnDisplay(work.Ctx, target)
	if readErr != nil || read.CandidateCount != 1 || read.Value == nil || *read.Value != text {
		var count *int
		if readErr == nil {
			count = &read.CandidateCount
		}
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"无法确认 ENTER 的目标输入对象", textStageEnter,
			textReasonEnterTargetUnconfirmed, OutcomeUncertain, count)
	}
	if e.workRevoked(work) || work.Ctx.Err() != nil {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageEnter, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	if !e.beginTextEnter(work, op) {
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageEnter, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	if apiErr := e.beginUIDispatchStage(work, "key", "enter"); apiErr != nil {
		if apiErr.Code == protocol.CodeStopped {
			return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
				"输入未派发：原工作已撤销", textStageEnter,
				textReasonAuthorizationRevoked, OutcomeUncertain, nil)
		}
		return OutcomeUncertain, e.textError(op, 504, protocol.CodeActionUnknown,
			"ENTER 未派发", textStageEnter, textReasonEnterNotSent,
			OutcomeUncertain, nil)
	}
	receipt, keyErr := e.dev.Key(work.Ctx, session.Display.ID, "ENTER")
	e.recordUIReceipt(work, receipt)
	if receipt == device.DispatchNotDispatched {
		e.commitTextReceipt(work, op, "enter", receipt, keyErr)
		return OutcomeUncertain, e.textError(op, 504, protocol.CodeActionUnknown,
			"ENTER 未派发", textStageEnter, textReasonEnterNotSent,
			OutcomeUncertain, nil)
	}
	if receipt == device.DispatchTerminated && keyErr == nil {
		e.commitTextReceipt(work, op, "enter", receipt, nil)
		if e.confirmTextEnter(work, op) {
			return OutcomeExecuted, nil
		}
		return OutcomeUncertain, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", textStageEnter, textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	e.commitTextReceipt(work, op, "enter", receipt, keyErr)
	return OutcomeUncertain, e.textError(op, 504, protocol.CodeActionUnknown,
		"输入已派发，但完成结果无法确认", textStageEnter,
		textReasonEnterUnconfirmed, OutcomeUncertain, nil)
}

func actionUncertain() *protocol.APIError {
	return &protocol.APIError{
		Code: protocol.CodeActionUnknown, Message: "输入已派发，但完成结果无法确认",
		HTTPStatus: 504, Outcome: OutcomeUncertain,
	}
}

func staticTextError(reason string) *protocol.APIError {
	message := "文字操作需要可确认的真实输入对象"
	if reason == textReasonTargetProtected {
		message = "无法确认该输入对象不受保护"
	}
	return &protocol.APIError{
		Code: protocol.CodeTextTargetUnknown, Message: message,
		HTTPStatus: 422, Outcome: OutcomeNotExecuted,
		Details: map[string]any{
			"stage":  textStageTarget,
			"reason": reason,
			"dispatch": map[string]any{
				"focus": textDispatchNotAttempted,
				"write": textDispatchNotAttempted,
				"enter": textDispatchNotAttempted,
			},
		},
	}
}

// decorateTextRevalidateError attaches the text target/focus detail group only
// to failures that are explicitly about this text target or its revocation.
// Public foreground, display and hierarchy errors keep their own contract.
func (e *Engine) decorateTextRevalidateError(op *operationState, apiErr *protocol.APIError) {
	if apiErr == nil || apiErr.Details != nil {
		return
	}
	var reason string
	switch apiErr.Code {
	case protocol.CodeTargetChanged:
		reason = textReasonTargetUnconfirmed
	case protocol.CodeStopped:
		reason = textReasonAuthorizationRevoked
	default:
		return
	}
	stage := e.textCurrentStage(op)
	apiErr.Details = e.textDetails(op, stage, reason, nil)
}

// textDetails builds the fixed core detail group from the frozen dispatch
// facts without changing the error or operation state.
func (e *Engine) textDetails(op *operationState, stage, reason string, count *int) map[string]any {
	e.mu.Lock()
	focus, write, enter := textStageSnapshotLocked(op)
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
	return details
}

type textBindingMode string

const (
	textExact   textBindingMode = "exact"
	textNear    textBindingMode = "near"
	textShifted textBindingMode = "shifted"
)

type textBinding struct {
	raw        *device.RawNode
	mode       textBindingMode
	anchor     optionalString
	anchorKind string
}

func textCandidateSafe(node *device.RawNode) bool {
	return node != nil && node.Visible &&
		node.HasFocused && node.Focused && node.Editable &&
		node.HasEnabled && node.Enabled &&
		node.HasPassword && !node.Password
}

func stableTextAnchor(raw *device.RawNode) (string, optionalString, bool) {
	if raw == nil {
		return "", optionalString{}, false
	}
	if raw.ResourceSet && raw.ResourceID != "" {
		return "resource", optionalString{set: true, value: raw.ResourceID}, true
	}
	if raw.DescSet && raw.Description != "" {
		return "description", optionalString{set: true, value: raw.Description}, true
	}
	return "", optionalString{}, false
}

func anchorCount(capture *device.Capture, want *device.RawNode,
	kind string, anchor optionalString) int {
	if capture == nil || want == nil || capture.Display.ID != want.DisplayID {
		return 0
	}
	count := 0
	for i := range capture.Nodes {
		node := &capture.Nodes[i]
		if node.DisplayID != want.DisplayID || !node.Editable ||
			node.Package != want.Package || node.ClassName != want.ClassName {
			continue
		}
		gotKind, gotAnchor, ok := stableTextAnchor(node)
		if ok && gotKind == kind && gotAnchor == anchor {
			count++
		}
	}
	return count
}

// sameTextSourcePresence deliberately omits Text/value: a successful set
// changes that value, and readback still has to identify the same source field.
func sameTextSourcePresence(want, node *device.RawNode) bool {
	if want == nil || node == nil ||
		want.Package != node.Package || want.ClassName != node.ClassName {
		return false
	}
	if want.ResourceSet != node.ResourceSet ||
		(want.ResourceSet && want.ResourceID != node.ResourceID) {
		return false
	}
	if want.DescSet != node.DescSet ||
		(want.DescSet && want.Description != node.Description) {
		return false
	}
	return true
}

func noAnchorNearCount(capture *device.Capture, want *device.RawNode,
	tapX, tapY int) int {
	if capture == nil || want == nil || capture.Display.ID != want.DisplayID {
		return 0
	}
	count := 0
	for i := range capture.Nodes {
		node := &capture.Nodes[i]
		if node.DisplayID != want.DisplayID || !node.Editable ||
			!sameTextSourcePresence(want, node) {
			continue
		}
		if node.Bounds.Contains(tapX, tapY) &&
			overlapRatio(node.Bounds, want.Bounds) >= 0.8 {
			count++
		}
	}
	return count
}

func sameHorizontalBand(a, b device.Rect) bool {
	if a.Width() <= 0 || b.Width() <= 0 || a.Height() <= 0 || b.Height() <= 0 {
		return false
	}
	tolX := max(8, max(a.Width(), b.Width())/50)
	return absInt(a.Left-b.Left) <= tolX &&
		absInt(a.Right-b.Right) <= tolX &&
		absInt(a.Width()-b.Width()) <= max(8, max(a.Width(), b.Width())/5) &&
		absInt(a.Height()-b.Height()) <= max(8, max(a.Height(), b.Height())/5)
}

func sameTextTaskContext(expected, fresh device.Foreground) bool {
	return foregroundMatches(expected, fresh) == nil &&
		expected.State == device.ForegroundPresent &&
		fresh.State == device.ForegroundPresent &&
		expected.TaskID != nil && fresh.TaskID != nil &&
		*expected.TaskID == *fresh.TaskID
}

// bindFocusedText binds one focused editable field to a stable source anchor
// and geometry. Exact and near are tried first; shifted permits only the
// vertical movement caused by an on-screen keyboard. Anchor uniqueness is
// counted before the focused-only safety filter, so a unique focus can never
// disambiguate two source fields.
func bindFocusedText(original, fresh *device.Capture, want *device.RawNode,
	expected device.Foreground, tapX, tapY int) (textBinding, bool) {
	if original == nil || fresh == nil || want == nil ||
		!want.Visible || !want.Editable ||
		!want.HasEnabled || !want.Enabled ||
		!want.HasPassword || want.Password {
		return textBinding{}, false
	}
	if original.Display.ID != fresh.Display.ID ||
		want.DisplayID != fresh.Display.ID ||
		!sameTextTaskContext(expected, fresh.Foreground) {
		return textBinding{}, false
	}
	kind, anchor, hasAnchor := stableTextAnchor(want)
	anchorUnique := false
	if hasAnchor {
		anchorUnique = anchorCount(original, want, kind, anchor) == 1 &&
			anchorCount(fresh, want, kind, anchor) == 1
		if !anchorUnique {
			return textBinding{}, false
		}
	} else if noAnchorNearCount(original, want, tapX, tapY) != 1 ||
		noAnchorNearCount(fresh, want, tapX, tapY) != 1 {
		return textBinding{}, false
	}

	var exact, near []*device.RawNode
	for i := range fresh.Nodes {
		node := &fresh.Nodes[i]
		if node.DisplayID != want.DisplayID || !textCandidateSafe(node) ||
			!sameTextSourcePresence(want, node) {
			continue
		}
		if hasAnchor {
			gotKind, gotAnchor, ok := stableTextAnchor(node)
			if !ok || gotKind != kind || gotAnchor != anchor {
				continue
			}
			if want.Bounds == node.Bounds {
				exact = append(exact, node)
			}
		}
		if node.Package == want.Package && node.Bounds.Contains(tapX, tapY) &&
			overlapRatio(node.Bounds, want.Bounds) >= 0.8 {
			near = append(near, node)
		}
	}
	switch {
	case len(exact) == 1:
		return textBinding{raw: exact[0], mode: textExact,
			anchor: anchor, anchorKind: kind}, true
	case len(exact) > 1:
		return textBinding{}, false
	case len(near) == 1:
		return textBinding{raw: near[0], mode: textNear}, true
	case len(near) > 1:
		return textBinding{}, false
	}
	if !hasAnchor || !anchorUnique {
		return textBinding{}, false
	}

	var shifted []*device.RawNode
	for i := range fresh.Nodes {
		node := &fresh.Nodes[i]
		if node.DisplayID != want.DisplayID || !textCandidateSafe(node) ||
			!sameTextSourcePresence(want, node) {
			continue
		}
		gotKind, gotAnchor, ok := stableTextAnchor(node)
		if !ok || gotKind != kind || gotAnchor != anchor ||
			!sameHorizontalBand(want.Bounds, node.Bounds) {
			continue
		}
		shifted = append(shifted, node)
	}
	if len(shifted) != 1 {
		return textBinding{}, false
	}
	return textBinding{raw: shifted[0], mode: textShifted,
		anchor: anchor, anchorKind: kind}, true
}

// bindFocusedTextAtPoint preserves the coordinate-text path: no ref anchor
// exists, so shifted is never enabled and only a unique safe focused field
// covering the authorized point can bind.
func bindFocusedTextAtPoint(capture *device.Capture, want *device.RawNode,
	tapX, tapY int) (*device.RawNode, bool) {
	if capture == nil {
		return nil, false
	}
	var strong []*device.RawNode
	var weak []*device.RawNode
	for i := range capture.Nodes {
		node := &capture.Nodes[i]
		if !node.Visible || !node.HasFocused || !node.Focused || !node.Editable {
			continue
		}
		if !node.HasEnabled || !node.Enabled {
			continue
		}
		if !node.HasPassword || node.Password {
			continue
		}
		if want != nil && want.Package != "" && node.Package != want.Package {
			continue
		}
		if want != nil && want.ResourceID != "" && node.ResourceID == want.ResourceID &&
			node.ClassName == want.ClassName && node.Description == want.Description {
			strong = append(strong, node)
			continue
		}
		if node.Bounds.Empty() || !node.Bounds.Contains(tapX, tapY) {
			continue
		}
		if want != nil && overlapRatio(node.Bounds, want.Bounds) >= 0.8 {
			weak = append(weak, node)
			continue
		}
		if want == nil {
			weak = append(weak, node)
		}
	}
	if len(strong) == 1 {
		return strong[0], true
	}
	if len(strong) == 0 && len(weak) == 1 {
		return weak[0], true
	}
	return nil, false
}

func overlapRatio(a, b device.Rect) float64 {
	intersection := a.Intersect(b)
	if intersection.Empty() {
		return 0
	}
	smaller := a.Width() * a.Height()
	if other := b.Width() * b.Height(); other < smaller {
		smaller = other
	}
	if smaller <= 0 {
		return 0
	}
	return float64(intersection.Width()*intersection.Height()) / float64(smaller)
}

func textTargetOf(node *device.RawNode, display device.Display) device.TextTarget {
	return device.TextTarget{
		DisplayID: display.ID, Package: node.Package, ResourceID: node.ResourceID,
		ClassName: node.ClassName, Description: node.Description,
		Bounds: node.Bounds, Width: display.Width, Height: display.Height,
	}
}

// rawIdentity and identityOf live in identity.go: live revalidation and the
// settle criterion must share exactly one identity function.

func lineageOf(capture *device.Capture, index int) []rawIdentity {
	var lineage []rawIdentity
	for parent := capture.Nodes[index].Parent; parent >= 0 && parent < len(capture.Nodes); parent = capture.Nodes[parent].Parent {
		lineage = append(lineage, identityOf(&capture.Nodes[parent]))
	}
	return lineage
}

func sameLineage(a, b []rawIdentity) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// matchFreshNode finds exactly one fresh node with identical content identity
// and identical ancestry. Ambiguity is a rejection, never a guess.
func matchFreshNode(fresh *device.Capture, original *device.Capture, raw *device.RawNode) (*device.RawNode, bool) {
	originalIndex := -1
	for i := range original.Nodes {
		if &original.Nodes[i] == raw {
			originalIndex = i
			break
		}
	}
	if originalIndex < 0 {
		return nil, false
	}
	want := identityOf(raw)
	wantLineage := lineageOf(original, originalIndex)
	var found *device.RawNode
	for i := range fresh.Nodes {
		node := &fresh.Nodes[i]
		if identityOf(node) != want {
			continue
		}
		if !sameLineage(lineageOf(fresh, i), wantLineage) {
			continue
		}
		if found != nil {
			return nil, false
		}
		found = node
	}
	return found, found != nil
}

func sourceNode(obs *observationState, ref string) *device.RawNode {
	if obs == nil || obs.Projection == nil || obs.Projection.Capture == nil {
		return nil
	}
	index, ok := obs.Projection.Source[ref]
	if !ok || index < 0 || index >= len(obs.Projection.Capture.Nodes) {
		return nil
	}
	return &obs.Projection.Capture.Nodes[index]
}

func findPublicNode(root *protocol.Node, ref string) *protocol.Node {
	if root == nil {
		return nil
	}
	if root.Ref == ref {
		return root
	}
	for _, child := range root.Children {
		if found := findPublicNode(child, ref); found != nil {
			return found
		}
	}
	return nil
}

func hasAction(node *protocol.Node, action protocol.Action) bool {
	for _, candidate := range node.Actions {
		if candidate == action {
			return true
		}
	}
	return false
}

func inScreen(x, y int, screen device.Display) bool {
	return x >= 0 && y >= 0 && x < screen.Width && y < screen.Height
}
