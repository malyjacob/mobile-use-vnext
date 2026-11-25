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
	var submit *textSubmitPlan
	if staticErr == nil {
		point, scroll, tap, submit, staticErr = staticActContentCheckWithTap(staticObs, &req.Operation)
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
		op.TextStageAutoSubmit = req.Operation.EffectiveAutoSubmit()
		e.initTextStageLocked(op, session)
	}
	e.touchSessionLocked(time.Now())
	// The composite text branch keeps one session for the whole flow. Snapshot
	// the locked session before unlocking so control sync cannot mutate Display
	// under it; the rest of the engine already works from sessionForWork copies.
	if session != nil {
		sessionSnapshot := *session
		session = &sessionSnapshot
	}
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
	e.logActPoint(op.ID, point, point != nil && (point.state == pointUnique || point.state == pointSet))

	e.setOperationPhase(op, "dispatch")
	var outcome string
	var dispatchErr *protocol.APIError
	var partialCapture *device.Capture
	switch {
	case scroll != nil:
		outcome, dispatchErr = e.dispatchScroll(work, frame)
	case req.Operation.Type == "text":
		// The composite text branch owns its own submission plan and may hand
		// back the last legal post-write frame for partial delivery.
		outcome, partialCapture, dispatchErr = e.dispatchText(work, op, session, obs,
			&req.Operation, fresh, tapFrame, submit)
	default:
		outcome, dispatchErr = e.dispatchAct(work, &req.Operation, tapFrame)
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
	if outcome == OutcomePartial {
		// A confirmed write whose submission certainly never left the engine is
		// neither executed nor uncertain: it is the honest partial fact, with no
		// unknown physical side effect. The delivery path clears the unknown
		// dispatch marker itself before publishing.
		return e.finishPartialText(work, op, session, partialCapture, dispatchErr, req.Image), nil
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
	public, postObs, postErr := e.captureAndPublish(work.Ctx, work, session, false, req.Image, "act", obs)
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
			if obs == nil || obs.Projection == nil || obs.Projection.Tree == nil ||
				obs.Projection.Tree.Root == nil {
				return nil, nil, tapHierarchyError()
			}
			node, ok := findPublicNodeBounded(obs.Projection.Tree.Root, operation.Target.Ref)
			if !ok {
				return nil, nil, tapHierarchyError()
			}
			if node == nil {
				return nil, nil, protocol.NewError(409, protocol.CodeNodeNotFound, "目标节点不存在")
			}
			if node.Role == protocol.RoleRoot {
				return nil, nil, newStaticActInvalidTargetError("root", node.Ref, "根节点不能作为操作目标")
			}
			if node.Context {
				return nil, nil, newStaticActInvalidTargetError("context", node.Ref, "上下文节点不能作为操作目标")
			}
			if node.Bounds == nil || node.Bounds.Width() <= 0 || node.Bounds.Height() <= 0 {
				return nil, nil, newStaticActInvalidTargetError("no_geometry", node.Ref, "目标节点没有可用几何")
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

func newStaticActInvalidTargetError(reason, ref, message string) *protocol.APIError {
	apiErr := protocol.NewError(422, protocol.CodeInvalidTarget, message)
	apiErr.WithDetail("reason", reason)
	apiErr.WithDetail("scope", "target")
	if ref != "" {
		apiErr.WithDetail("scope_ref", ref)
	}
	return apiErr
}

// staticActContentCheckWithTap routes tap, long_press and text ref targets to
// their bounded planners while keeping coordinate, swipe, scroll and key
// validation on staticActContentCheck. A text operation whose effective
// submit_ref names a public node additionally plans that submit tap here, so a
// missing or unsuitable submit target still rejects before the observation is
// claimed and no device I/O happens.
func staticActContentCheckWithTap(obs *observationState, operation *protocol.Operation) (
	*pointCheck, *scrollPlan, *tapPlan, *textSubmitPlan, *protocol.APIError) {
	if operation != nil && operation.Target != nil && operation.Target.UseRef {
		switch operation.Type {
		case "tap", "long_press":
			point, scroll, tap, apiErr := staticTapRefCheck(obs, operation)
			return point, scroll, tap, nil, apiErr
		case "text":
			point, scroll, tap, apiErr := staticTextRefCheck(obs, operation)
			if apiErr != nil {
				return point, scroll, tap, nil, apiErr
			}
			submit, apiErr := planTextSubmitRef(obs, operation)
			return point, scroll, tap, submit, apiErr
		}
	}
	point, scroll, apiErr := staticActContentCheck(obs, operation)
	if apiErr != nil {
		return point, scroll, nil, nil, apiErr
	}
	var submit *textSubmitPlan
	if operation != nil && operation.Type == "text" {
		submit, apiErr = planTextSubmitRef(obs, operation)
	}
	return point, scroll, nil, submit, apiErr
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
	if obs.Full && work.Kind == WorkAct {
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
	return e.probeActNodeWith(ctx, revoked, original, fresh, want,
		defaultActMatchOptions())
}

// probeActNodeWith probes one ref binding under an explicit match policy. STOP
// is checked before the matcher, at every Tier boundary inside it, and once more
// before the result reaches the caller.
func (e *Engine) probeActNodeWith(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode,
	opts actMatchOptions) (actMatch, actProbeState) {
	if ctx == nil || ctx.Err() != nil || (revoked != nil && revoked()) {
		return actMatch{}, actProbeCancelled
	}
	match, state := matchActTargetWithOptions(ctx, revoked, original, fresh, want, opts)
	if ctx.Err() != nil || (revoked != nil && revoked()) {
		return actMatch{}, actProbeCancelled
	}
	return match, state
}

// tapFrameOptions is the optional policy of one tap-frame revalidation. The zero
// value is the ordinary tap/long_press contract: Tier 1-4 with no enabled
// transition, no soft-keyboard shift and no write-submit classification.
type tapFrameOptions struct {
	// match selects the target policy of every binding probe. Its
	// allowEnabledTransition field is applied to the execution binding only.
	match actMatchOptions
	// submit enables the write-submit classifications: an execution target that
	// is still explicitly disabled is reported as submit_disabled instead of a
	// generic capability change. It never relaxes the enabled gate.
	submit bool
	// requireExecutionEnabled demands an explicitly enabled execution target on
	// the fresh frame because the write-time plan observed it disabled.
	requireExecutionEnabled bool
	// keyboardShift enables the bounded soft-keyboard vertical-shift fallback for
	// the plan's binding nodes.
	keyboardShift bool
}

// submitTapFrameOptions is the complete write-submit revalidation policy.
func submitTapFrameOptions(initiallyDisabled bool) tapFrameOptions {
	options := tapFrameOptions{
		submit:        true,
		keyboardShift: true,
	}
	if initiallyDisabled {
		options.match = options.match.withEnabledTransition()
		options.requireExecutionEnabled = true
	}
	return options
}

// probeTapFrameNode probes one binding node of a tap-frame plan under the frame
// policy. Only the execution binding may accept the disabled -> enabled
// transition; every other binding keeps the ordinary structure comparison. The
// soft-keyboard fallback applies to each binding because the whole page moves.
func (e *Engine) probeTapFrameNode(ctx context.Context, revoked func() bool,
	original, fresh *device.Capture, want *device.RawNode,
	execution bool, opts tapFrameOptions) (actMatch, actProbeState) {
	matchOpts := opts.match
	if !execution {
		matchOpts.allowEnabledTransition = false
	}
	if ctx == nil || ctx.Err() != nil || (revoked != nil && revoked()) {
		return actMatch{}, actProbeCancelled
	}
	match, state := matchActTargetWithOptions(ctx, revoked, original, fresh, want, matchOpts)
	if state == actProbeNone && opts.keyboardShift {
		shifted, shiftedState := matchKeyboardShiftedSubmitNode(ctx, revoked,
			original, fresh, want, matchOpts.allowEnabledTransition, matchOpts.limits)
		switch shiftedState {
		case actProbeNone:
		case actProbeUnique:
			match, state = shifted, shiftedState
		default:
			return shifted, shiftedState
		}
	}
	if ctx.Err() != nil || (revoked != nil && revoked()) {
		return actMatch{}, actProbeCancelled
	}
	return match, state
}

// framePublicTargetError applies the ordinary public action gate and, for a
// write-submit plan, classifies an explicitly disabled execution target as
// submit_disabled. A disabled target is still rejected: the classification only
// names the reason.
func framePublicTargetError(node *protocol.Node, action protocol.Action,
	execution bool, opts tapFrameOptions) *protocol.APIError {
	if validTapPublicTarget(node, action) {
		return nil
	}
	if execution && opts.submit && explicitlyDisabledPublic(node) {
		return submitFrameDisabledError()
	}
	return tapTargetChangedReason(targetChangedReasonCapabilityChanged)
}

// explicitlyDisabledPublic reports an explicitly disabled public target. A
// missing flag is never treated as disabled here.
func explicitlyDisabledPublic(node *protocol.Node) bool {
	return node != nil && node.Enabled != nil && !*node.Enabled
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
	if point != nil && (point.state == pointUnique || point.state == pointSet ||
		(point.state == pointNone && point.empty)) {
		revokedPoint := func() bool { return e.workRevoked(work) }
		if apiErr := e.provePointTarget(work.Ctx, revokedPoint, work.Projector,
			capture, obs, point, operation); apiErr != nil {
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
	if frame != nil {
		e.actTargetLog(work.OperationID, actTargetProbeOutcome(actProbeUnique), frame.tier)
	}
	if operation.Type == "text" {
		if frame == nil || !freshTextTargetSafe(frame.raw) {
			return capture, nil, took, tapTargetChangedReason(targetChangedReasonCapabilityChanged)
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

const targetChangedMessage = "目标元素已变化，本次未派发"

func tapTargetChangedReason(reason string) *protocol.APIError {
	return targetChangedError(reason, targetChangedMessage)
}

func sameNonCapabilityStructure(leftNode, rightNode *device.RawNode) (sameIdentity, capabilityChanged bool) {
	if leftNode == nil || rightNode == nil {
		return false, false
	}
	if leftNode.DisplayID != rightNode.DisplayID ||
		leftNode.Package != rightNode.Package ||
		leftNode.ClassName != rightNode.ClassName {
		return false, false
	}
	left := structureKeyOf(leftNode)
	right := structureKeyOf(rightNode)
	if compareOptional(left.Text, right.Text) == triMismatch ||
		compareOptional(left.Description, right.Description) == triMismatch ||
		compareOptional(left.Resource, right.Resource) == triMismatch {
		return false, false
	}
	if compareTri(left.Checked, right.Checked) == triMismatch ||
		compareTri(left.Checkable, right.Checkable) == triMismatch {
		return false, false
	}
	if left.Editable != right.Editable {
		return true, true
	}
	for _, pair := range [][2]tri{
		{left.Clickable, right.Clickable},
		{left.LongClickable, right.LongClickable},
		{left.Scrollable, right.Scrollable},
		{left.Enabled, right.Enabled},
		{left.Password, right.Password},
	} {
		if compareTri(pair[0], pair[1]) == triMismatch {
			return true, true
		}
	}
	return true, false
}

func equalLineageEntries(left, right []lineageEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].root != right[index].root || left[index].key != right[index].key {
			return false
		}
	}
	return true
}

func sameRawLineageKeys(original *device.Capture, oldIndex int,
	fresh *device.Capture, newIndex int, budget *matchBudget) bool {
	if original == nil || fresh == nil || budget == nil ||
		oldIndex < 0 || oldIndex >= len(original.Nodes) ||
		newIndex < 0 || newIndex >= len(fresh.Nodes) {
		return false
	}
	oldL1, ok := lineageL1(original, oldIndex, budget)
	if !ok {
		return false
	}
	newL1, ok := lineageL1(fresh, newIndex, budget)
	if !ok {
		return false
	}
	if equalLineageEntries(oldL1, newL1) {
		return true
	}
	oldL2, ok := lineageL2(original, oldIndex, budget)
	if !ok {
		return false
	}
	newL2, ok := lineageL2(fresh, newIndex, budget)
	if !ok {
		return false
	}
	return equalLineageEntries(oldL2, newL2)
}

func hasCapabilityOnlyFreshNode(ctx context.Context, original, fresh *device.Capture,
	want *device.RawNode) (bool, bool) {
	if original == nil || fresh == nil || want == nil {
		return false, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	wantIndex := indexOf(original, want)
	if wantIndex < 0 {
		return false, false
	}
	screen := screenRectFromDisplay(fresh.Display)
	limits := defaultActMatchLimits()
	budget := &matchBudget{
		ctx:            ctx,
		candidateLimit: len(fresh.Nodes) + len(original.Nodes),
		lineageLimit:   limits.lineageLimit,
		lineageDepth:   limits.lineageDepth,
	}
	matchedCount := 0
	for freshIndex := range fresh.Nodes {
		if !budget.consumeCandidate() {
			return false, budget.stop == matchCancelled
		}
		candidate := &fresh.Nodes[freshIndex]
		sameIdentity, capChanged := sameNonCapabilityStructure(want, candidate)
		if !sameIdentity || !capChanged {
			continue
		}
		if want.Bounds != candidate.Bounds &&
			!geometryCompatible(want.Bounds, candidate.Bounds, screen, actTier2MinOverlap) {
			continue
		}
		if !sameRawLineageKeys(original, wantIndex, fresh, freshIndex, budget) {
			if budget.stop != matchProceed {
				return false, budget.stop == matchCancelled
			}
			continue
		}
		matchedCount++
		if matchedCount > 1 {
			return false, false
		}
	}
	return matchedCount == 1, false
}

func probeTapTargetChangedError(ctx context.Context, obs *observationState,
	fresh *device.Capture, want *device.RawNode, state actProbeState, match actMatch) *protocol.APIError {
	var original *device.Capture
	if obs != nil && obs.Projection != nil {
		original = obs.Projection.Capture
	}
	if state == actProbeNone && !match.lineageMismatch {
		capChanged, cancelled := hasCapabilityOnlyFreshNode(ctx, original, fresh, want)
		if cancelled {
			return stoppedActRevalidation()
		}
		if capChanged {
			return tapTargetChangedReason(targetChangedReasonCapabilityChanged)
		}
	}
	apiErr := probeTargetChangedError(state, match, targetChangedMessage)
	attachChangedRef(ctx, apiErr, obs, fresh, want)
	return apiErr
}

// revalidateBubbleTapFrame re-proves both identities of a C->A target lift on
// the fresh frame. The child stays the authorization selector: if it is lost,
// even a uniquely matched ancestor cannot silently inherit the action.
func (e *Engine) revalidateBubbleTapFrame(ctx context.Context,
	revoked func() bool, workProjector Projector, obs *observationState,
	fresh *device.Capture, plan *tapPlan,
	opts tapFrameOptions) (*tapFrame, *protocol.APIError) {
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
	matchChild, childState := e.probeTapFrameNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.targetRaw, false, opts)
	switch childState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	default:
		return nil, tapHierarchyError()
	}
	matchAncestor, ancestorState := e.probeTapFrameNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.bubbleAncestorRaw, true, opts)
	switch ancestorState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, probeTapTargetChangedError(
			ctx, obs, fresh, plan.bubbleAncestorRaw, ancestorState, matchAncestor)
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
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	case tapSourceDamaged:
		return nil, tapHierarchyError()
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	childPublic := view.byRaw[childIndex]
	if childPublic == nil {
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	}
	if obs.Projection.Tree == nil || obs.Projection.Tree.Root == nil {
		return nil, tapHierarchyError()
	}
	originalPublic, ok := findPublicNodeBounded(obs.Projection.Tree.Root, plan.targetRef)
	if !ok {
		return nil, tapHierarchyError()
	}
	if originalPublic == nil {
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	}
	if childPublic.Role != originalPublic.Role ||
		!samePublicLabel(childPublic.Label, originalPublic.Label) {
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	}
	if isTouchInteractive(childPublic) {
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	}
	ancestorPublic := view.byRaw[ancestorIndex]
	if ancestorPublic == nil {
		return nil, tapTargetChangedReason(targetChangedReasonCapabilityChanged)
	}
	if !topology.isAncestor(ancestorIndex, childIndex) {
		return nil, bubbleRelationChangedError(plan.bubbleAncestorRef)
	}
	if nearestInteractiveAncestor(view, childPublic) != ancestorPublic {
		return nil, bubbleRelationChangedError(plan.bubbleAncestorRef)
	}
	if apiErr := framePublicTargetError(ancestorPublic,
		protocol.Action(plan.action), true, opts); apiErr != nil {
		return nil, apiErr
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
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	x, y, strategy, carrier, ok := chooseBubbleTapPoint(scope, view, ancestorPublic, childPublic)
	if !ok {
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	if matchAncestor.tier == 4 &&
		!tapFrameTier4OcclusionProven(view, ancestorPublic, x, y) {
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
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

// revalidateCoextensiveChildFrame re-proves both identities of a C->B
// coextensive child delegation on the fresh frame before tapping inside B.
func (e *Engine) revalidateCoextensiveChildFrame(ctx context.Context,
	revoked func() bool, workProjector Projector, obs *observationState,
	fresh *device.Capture, plan *tapPlan,
	opts tapFrameOptions) (*tapFrame, *protocol.APIError) {
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
	if stop() {
		return nil, stoppedActRevalidation()
	}
	matchParent, parentState := e.probeTapFrameNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.targetRaw, false, opts)
	switch parentState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, probeTapTargetChangedError(
			ctx, obs, fresh, plan.targetRaw, parentState, matchParent)
	default:
		return nil, tapHierarchyError()
	}
	matchDelegate, delegateState := e.probeTapFrameNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.delegateRaw, true, opts)
	switch delegateState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, probeTapTargetChangedError(
			ctx, obs, fresh, plan.delegateRaw, delegateState, matchDelegate)
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
	parentIndex := indexOf(fresh, matchParent.raw)
	delegateIndex := indexOf(fresh, matchDelegate.raw)
	if parentIndex < 0 || delegateIndex < 0 {
		return nil, tapHierarchyError()
	}
	switch checkTapSourceMapping(view, topology, parentIndex) {
	case tapSourceTargetMissing:
		return nil, tapTargetChangedReason(targetChangedReasonCapabilityChanged)
	case tapSourceDamaged:
		return nil, tapHierarchyError()
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	freshParent := view.byRaw[parentIndex]
	freshDelegate := view.byRaw[delegateIndex]
	if freshParent == nil || freshDelegate == nil {
		return nil, tapTargetChangedReason(targetChangedReasonCapabilityChanged)
	}
	action := protocol.Action(plan.action)
	if apiErr := framePublicTargetError(freshParent, action, false, opts); apiErr != nil {
		return nil, apiErr
	}
	if apiErr := framePublicTargetError(freshDelegate, action, true, opts); apiErr != nil {
		return nil, apiErr
	}
	parentInput := &tapInput{
		view: view, capture: fresh, topology: topology,
		target: freshParent, targetRaw: matchParent.raw, targetIndex: parentIndex,
	}
	parentScope, apiErr := collectTapScope(parentInput)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if apiErr != nil {
		if apiErr.Code == protocol.CodeHierarchyFailed {
			return nil, apiErr
		}
		return nil, tapTargetChangedReason(targetChangedReasonCapabilityChanged)
	}
	screen := screenRectFromDisplay(fresh.Display)
	if !isTopLevelCoextensiveBlocker(parentScope, view, freshParent, freshDelegate, screen) {
		return nil, tapTargetChangedReason(targetChangedReasonCapabilityChanged)
	}
	delegateInput := &tapInput{
		view: view, capture: fresh, topology: topology,
		target: freshDelegate, targetRaw: matchDelegate.raw, targetIndex: delegateIndex,
	}
	delegateScope, apiErr := collectTapScope(delegateInput)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if apiErr != nil {
		if apiErr.Code == protocol.CodeHierarchyFailed {
			return nil, apiErr
		}
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	return e.resolveDirectTapScopeFrame(ctx, revoked, obs, fresh, topology, view,
		plan, matchDelegate, freshDelegate, delegateIndex, delegateInput,
		delegateScope, opts)
}

// revalidateColocatedTapFrame re-proves both identities and the co-location
// guard of a C->B bubble_colocated plan on the fresh frame.
func (e *Engine) revalidateColocatedTapFrame(ctx context.Context,
	revoked func() bool, workProjector Projector, obs *observationState,
	fresh *device.Capture, plan *tapPlan,
	opts tapFrameOptions) (*tapFrame, *protocol.APIError) {
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
	if stop() {
		return nil, stoppedActRevalidation()
	}
	matchChild, childState := e.probeTapFrameNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.targetRaw, false, opts)
	switch childState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	default:
		return nil, tapHierarchyError()
	}
	matchColocated, colocatedState := e.probeTapFrameNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.colocatedRaw, true, opts)
	switch colocatedState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, probeTapTargetChangedError(
			ctx, obs, fresh, plan.colocatedRaw, colocatedState, matchColocated)
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
	colocatedIndex := indexOf(fresh, matchColocated.raw)
	if childIndex < 0 || colocatedIndex < 0 {
		return nil, tapHierarchyError()
	}
	switch checkTapSourceMapping(view, topology, childIndex) {
	case tapSourceTargetMissing:
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	case tapSourceDamaged:
		return nil, tapHierarchyError()
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	freshChild := view.byRaw[childIndex]
	if freshChild == nil {
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	}
	if obs.Projection.Tree == nil || obs.Projection.Tree.Root == nil {
		return nil, tapHierarchyError()
	}
	originalChild, ok := findPublicNodeBounded(obs.Projection.Tree.Root, plan.targetRef)
	if !ok {
		return nil, tapHierarchyError()
	}
	if originalChild == nil {
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	}
	if freshChild.Role != originalChild.Role ||
		!samePublicLabel(freshChild.Label, originalChild.Label) ||
		isTouchInteractive(freshChild) {
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	}
	freshColocated := view.byRaw[colocatedIndex]
	if freshColocated == nil {
		return nil, tapTargetChangedReason(targetChangedReasonCapabilityChanged)
	}
	if freshColocated == freshChild ||
		isPublicAncestor(view, freshColocated, freshChild) {
		return nil, tapTargetChangedReason(targetChangedReasonCapabilityChanged)
	}
	if apiErr := framePublicTargetError(freshColocated,
		protocol.Action(plan.action), true, opts); apiErr != nil {
		return nil, apiErr
	}
	screen := screenRectFromDisplay(fresh.Display)
	if !colocatedMatchGuard(view, freshChild, freshColocated, screen) {
		return nil, tapTargetChangedReason(targetChangedReasonChildChanged)
	}
	colocatedInput := &tapInput{
		view: view, capture: fresh, topology: topology,
		target: freshColocated, targetRaw: matchColocated.raw, targetIndex: colocatedIndex,
	}
	colocatedScope, apiErr := collectTapScope(colocatedInput)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if apiErr != nil {
		if apiErr.Code == protocol.CodeHierarchyFailed {
			return nil, apiErr
		}
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	fixedPointOK := false
	var fixedPointCarrier *tapCarrier
	if carrier, ok := pointOwner(colocatedScope, plan.x, plan.y); ok {
		fixedPointOK = true
		fixedPointCarrier = carrier
		if plan.colocatedInChild {
			if freshChild.Bounds == nil {
				fixedPointOK = false
			} else {
				childClip := rectFromBounds(freshChild.Bounds).Intersect(colocatedScope.base)
				if childClip.Empty() || !childClip.Contains(plan.x, plan.y) {
					fixedPointOK = false
				}
			}
		}
	}
	if fixedPointOK {
		if stop() {
			return nil, stoppedActRevalidation()
		}
		if matchColocated.tier == 4 &&
			!tapFrameTier4OcclusionProven(view, freshColocated, plan.x, plan.y) {
			return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
		}
		return &tapFrame{
			capture: fresh, raw: matchColocated.raw, x: plan.x, y: plan.y,
			carrier: carrierRaw(fixedPointCarrier), tier: matchColocated.tier,
			strategy: inputPlanStrategyForPoint(colocatedScope, fixedPointCarrier),
		}, nil
	}
	x, y, strategy, carrier, _, ok := chooseColocatedTapPoint(
		colocatedScope, view, freshChild, freshColocated, screen)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if !ok || !provesSubtreeTapPoint(colocatedScope, x, y) {
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	if matchColocated.tier == 4 &&
		!tapFrameTier4OcclusionProven(view, freshColocated, x, y) {
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	return &tapFrame{
		capture: fresh, raw: matchColocated.raw, x: x, y: y,
		carrier: carrierRaw(carrier), tier: matchColocated.tier, strategy: strategy,
	}, nil
}

// revalidateTapTargetFrame proves one fresh ref target and computes the exact
// point dispatch is allowed to use. The returned frame is immutable: target,
// carrier, coordinates, Tier and strategy are fixed before the first primitive.
func (e *Engine) revalidateTapTargetFrame(ctx context.Context,
	revoked func() bool, workProjector Projector, obs *observationState,
	fresh *device.Capture, plan *tapPlan) (*tapFrame, *protocol.APIError) {
	return e.revalidateTapTargetFrameWith(ctx, revoked, workProjector, obs, fresh,
		plan, tapFrameOptions{})
}

// revalidateSubmitTapFrame is the write-submit entry: it re-proves the static
// submit tap plan on the post-write frame with the submit policy and refuses a
// disabled execution target. It reuses every four-mode relationship guard and
// recomputes the landing point exactly like an ordinary tap. The returned error
// keeps the ordinary code; a still-disabled execution target carries the
// submit_disabled classification.
func (e *Engine) revalidateSubmitTapFrame(ctx context.Context, revoked func() bool,
	workProjector Projector, obs *observationState, fresh *device.Capture,
	plan *tapPlan, initiallyDisabled bool) (*tapFrame, *protocol.APIError) {
	frame, apiErr := e.revalidateTapTargetFrameWith(ctx, revoked, workProjector,
		obs, fresh, plan, submitTapFrameOptions(initiallyDisabled))
	if apiErr != nil {
		// When the target itself cannot be matched any more, the only extra
		// distinction this entry adds is the reason: a uniquely attributable
		// explicitly disabled execution target is reported as submit_disabled.
		// The frame is still rejected; nothing here authorizes dispatch.
		if apiErr.Code == protocol.CodeTargetChanged &&
			apiErr.Details["reason"] != targetChangedReasonScopeBlocked &&
			obs != nil && obs.Projection != nil &&
			submitExecutionDisabledOnFresh(fresh, tapPlanExecutionRaw(plan)) {
			return nil, submitFrameDisabledError()
		}
		return nil, apiErr
	}
	if !submitExecutionEnabled(frame, initiallyDisabled) {
		return nil, submitFrameDisabledError()
	}
	return frame, nil
}

// revalidateTapTargetFrameWith re-proves one tap-frame plan under an explicit
// policy. Only the execution binding of a write-submit plan receives the enabled
// transition and the submit classification; every relationship guard, scope
// proof and landing-point recomputation is shared with the ordinary tap path.
func (e *Engine) revalidateTapTargetFrameWith(ctx context.Context,
	revoked func() bool, workProjector Projector, obs *observationState,
	fresh *device.Capture, plan *tapPlan,
	opts tapFrameOptions) (*tapFrame, *protocol.APIError) {
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
		return e.revalidateBubbleTapFrame(ctx, revoked, workProjector, obs, fresh, plan, opts)
	}
	if plan.colocatedRaw != nil {
		return e.revalidateColocatedTapFrame(ctx, revoked, workProjector, obs, fresh, plan, opts)
	}
	if plan.delegateRaw != nil {
		return e.revalidateCoextensiveChildFrame(ctx, revoked, workProjector, obs, fresh, plan, opts)
	}
	topology, apiErr := validateTapRawTopology(fresh)
	if apiErr != nil {
		return nil, apiErr
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}

	match, matchState := e.probeTapFrameNode(
		ctx, revoked, obs.Projection.Capture, fresh, plan.targetRaw, true, opts)
	switch matchState {
	case actProbeUnique:
	case actProbeCancelled:
		return nil, stoppedActRevalidation()
	case actProbeBudgetExhausted, actProbeMalformed:
		return nil, tapHierarchyError()
	case actProbeNone, actProbeAmbiguous:
		return nil, probeTapTargetChangedError(
			ctx, obs, fresh, plan.targetRaw, matchState, match)
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
		return nil, tapTargetChangedReason(targetChangedReasonCapabilityChanged)
	case tapSourceDamaged:
		return nil, tapHierarchyError()
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	targetPublic := view.byRaw[targetIndex]
	if apiErr := framePublicTargetError(targetPublic,
		protocol.Action(plan.action), true, opts); apiErr != nil {
		return nil, apiErr
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
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	return e.resolveDirectTapScopeFrame(ctx, revoked, obs, fresh, topology, view,
		plan, match, targetPublic, targetIndex, input, scope, opts)
}

// directFrameOcclusionProven guards the direct and coextensive landing points
// resolved by resolveDirectTapScopeFrame. A Tier 4 execution target lost its
// stable ancestry, so the subtree scope alone no longer proves the point; a
// newly inserted external owner or a deepest hit outside the target subtree
// rejects the frame. Tier 1-3 frames are never affected.
func directFrameOcclusionProven(match actMatch, view *tapPublicView,
	target *protocol.Node, x, y int) bool {
	return match.tier != 4 || tapFrameTier4OcclusionProven(view, target, x, y)
}

func (e *Engine) resolveDirectTapScopeFrame(ctx context.Context,
	revoked func() bool, obs *observationState, fresh *device.Capture,
	topology *tapRawTopology, view *tapPublicView, plan *tapPlan,
	match actMatch, targetPublic *protocol.Node, targetIndex int,
	input *tapInput, scope *tapScope, opts tapFrameOptions) (*tapFrame, *protocol.APIError) {
	stop := func() bool {
		return ctx == nil || ctx.Err() != nil || (revoked != nil && revoked())
	}
	targetRaw := match.raw
	originalPointCarrier, originalPointOK := pointOwner(scope, plan.x, plan.y)

	var fixedCarrier *tapCarrier
	if plan.carrierRaw != nil {
		carrierMatch, carrierState := e.probeTapFrameNode(
			ctx, revoked, obs.Projection.Capture, fresh, plan.carrierRaw, false, opts)
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
		if !directFrameOcclusionProven(match, view, targetPublic, plan.x, plan.y) {
			return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
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
			if !directFrameOcclusionProven(match, view, targetPublic, x, y) {
				return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
			}
			return &tapFrame{
				capture: fresh, raw: targetRaw, x: x, y: y,
				carrier: fixedCarrier.raw, tier: match.tier, strategy: strategy,
			}, nil
		}
	}

	if plan.carrierRaw != nil && !match.independentContent {
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	unbound, apiErr := chooseUnboundTapPlan(
		scope, input, targetPublic, protocol.Action(plan.action), plan.x, plan.y)
	if stop() {
		return nil, stoppedActRevalidation()
	}
	if apiErr != nil || unbound == nil || unbound.strategy == tapModeRejected {
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	if !provesSubtreeTapPoint(scope, unbound.x, unbound.y) {
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	if !directFrameOcclusionProven(match, view, targetPublic, unbound.x, unbound.y) {
		return nil, tapTargetChangedReason(targetChangedReasonScopeBlocked)
	}
	if stop() {
		return nil, stoppedActRevalidation()
	}
	return &tapFrame{
		capture: fresh, raw: targetRaw, x: unbound.x, y: unbound.y,
		carrier: unbound.carrierRaw, tier: match.tier, strategy: unbound.strategy,
	}, nil
}

// provePointTarget re-proves the coordinate target on the fresh frame. The
// fresh deepest hit set must map one-to-one onto raw indices whose count equals
// the observed set, and a bounded bipartite maximum matching must pair every
// observed node with a distinct fresh node:
//
//   - bare swipe: structural compatibility plus an anti-occlusion geometry gate,
//     deliberately ignoring passive leaf text and ancestor wrappers;
//   - coordinate tap/long_press/text: the target's own structure stays strictly
//     equal, while ancestors and geometry follow the ref tiers, and the fresh
//     point must not introduce a new competing interactive owner.
//
// The projection is pure CPU and never advances ref continuity.
func (e *Engine) provePointTarget(ctx context.Context, revoked func() bool,
	workProjector Projector, fresh *device.Capture, obs *observationState,
	point *pointCheck, operation *protocol.Operation) *protocol.APIError {
	if point == nil || obs == nil || obs.Projection == nil || obs.Projection.Capture == nil {
		return targetChangedAtPoint()
	}
	started := time.Now()
	finish := func(apiErr *protocol.APIError) *protocol.APIError {
		point.took += time.Since(started)
		return apiErr
	}
	if _, apiErr := validateTapRawTopology(fresh); apiErr != nil {
		return finish(apiErr)
	}
	observedIndices := point.indices
	if len(observedIndices) == 0 && point.index >= 0 {
		observedIndices = []int{point.index}
	}
	emptyPoint := len(observedIndices) == 0
	if !emptyPoint && len(observedIndices) > maxPointHitSet {
		return finish(targetChangedAtPoint())
	}
	projector := workProjector
	if projector == nil {
		projector = e.projector
	}
	if projector == nil {
		return finish(protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败"))
	}
	projection, err := projector.Preview(fresh)
	if err != nil || projection == nil || projection.Tree == nil ||
		projection.Tree.Root == nil || projection.Source == nil {
		return finish(protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败"))
	}
	freshNodes := resolvePointSet(projection.Tree.Root, point.x, point.y)
	if len(freshNodes) != len(observedIndices) {
		return finish(targetChangedAtPoint())
	}
	if emptyPoint {
		// A blank point stays authorized only while it is still blank. A node
		// or overlay that now covers it would receive an input the caller never
		// aimed at, so the proof fails exactly like a changed hit set.
		return finish(nil)
	}
	freshIndices := make([]int, 0, len(freshNodes))
	seen := make(map[int]struct{}, len(freshNodes))
	for _, node := range freshNodes {
		if node == nil || node.Ref == "" {
			return finish(targetChangedAtPoint())
		}
		index, mapped := projection.Source[node.Ref]
		if !mapped || index < 0 || index >= len(fresh.Nodes) {
			return finish(targetChangedAtPoint())
		}
		if _, duplicate := seen[index]; duplicate {
			return finish(targetChangedAtPoint())
		}
		seen[index] = struct{}{}
		freshIndices = append(freshIndices, index)
	}
	proof := newPointProof(ctx, revoked, obs.Projection.Capture, fresh,
		fresh.Display.Bounds())
	if operation != nil && operation.Type == "swipe" {
		return finish(proof.proveSwipeSet(observedIndices, freshIndices, point.x, point.y))
	}
	view, viewErr := buildTapPublicView(projection.Tree, fresh, projection.Source)
	if viewErr != nil {
		if viewErr.Code == protocol.CodeHierarchyFailed {
			return finish(viewErr)
		}
		return finish(targetChangedAtPoint())
	}
	return finish(proof.proveTargetSet(observedIndices, freshIndices, point.x, point.y,
		view, freshNodes, operation))
}

func targetChangedAtPoint() *protocol.APIError {
	return targetChangedError(targetChangedReasonPointChanged, "目标点上的节点已变化，本次未派发")
}

// logActPoint records one coordinate-target decision. Only the operation, the
// resolution state, whether the node-level proof succeeded and the pure-CPU
// cost are written; the point, refs, labels and tree content never are.
func (e *Engine) logActPoint(operationID string, point *pointCheck, proven bool) {
	if point == nil {
		return
	}
	fields := []eventlog.Field{
		eventlog.Str("operation_id", operationID),
		eventlog.Str("state", string(point.state)),
		eventlog.Bool("proven", proven),
		eventlog.Duration("ms", point.took),
	}
	if point.mode != "" {
		fields = append(fields, eventlog.Str("mode", point.mode))
	}
	e.log.Event(eventlog.Debug, "act.point", fields...)
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

// dispatchAct executes exactly one non-text input branch and reports whether
// the device accepted it. Anything after the first dispatched primitive that
// cannot be confirmed is uncertain; nothing is replayed. Text is a composite
// branch with its own dispatchText entry.
func (e *Engine) dispatchAct(work *workState, operation *protocol.Operation,
	frame *tapFrame) (string, *protocol.APIError) {
	session := e.sessionForWork(work)
	if session == nil || e.workRevoked(work) {
		return OutcomeNotExecuted, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	displayID := session.Display.ID
	x, y := 0, 0
	if operation.Target != nil {
		x, y = operation.Target.X, operation.Target.Y
	}
	if operation.Target != nil && operation.Target.UseRef {
		if frame == nil || (operation.Type == "tap" || operation.Type == "long_press") == false {
			return OutcomeNotExecuted, tapTargetChangedReason(targetChangedReasonTargetMissing)
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
// independent submission. A ref that is already focused in the fresh frame and
// passes the complete binding gate skips the focus tap. The set call's own
// refreshed value of the same node is the write completion evidence; no second
// tree acquisition or GetText is used to re-prove the write. The set is never
// replayed and text is never rolled back. A confirmed write whose submission
// definitely never left the engine returns OutcomePartial with the last fresh
// capture for on-the-spot delivery.
func (e *Engine) dispatchText(work *workState, op *operationState, session *sessionState,
	obs *observationState, operation *protocol.Operation,
	fresh *device.Capture, frame *tapFrame,
	submit *textSubmitPlan) (string, *device.Capture, *protocol.APIError) {
	if operation == nil || operation.Target == nil || fresh == nil {
		return OutcomeNotExecuted, nil, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"无法唯一绑定目标输入对象", textStageTarget, textReasonTargetUnconfirmed,
			OutcomeNotExecuted, nil)
	}
	autoSubmit := operation.EffectiveAutoSubmit()

	var bound textBinding
	var apiErr *protocol.APIError
	if operation.Target.UseRef {
		if frame == nil || obs == nil || obs.Projection == nil || obs.Projection.Capture == nil {
			return OutcomeNotExecuted, nil, e.textError(op, 409, protocol.CodeTextTargetUnknown,
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
				return apiErr.Outcome, nil, apiErr
			}
		}
	} else {
		tapX, tapY := operation.Target.X, operation.Target.Y
		bound, apiErr = e.focusTextPoint(work, op, session, fresh, tapX, tapY)
		if apiErr != nil {
			return apiErr.Outcome, nil, apiErr
		}
	}
	if bound.raw == nil || bound.capture == nil {
		return OutcomeNotExecuted, nil, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"无法唯一绑定目标输入对象", textStageTarget, textReasonTargetUnconfirmed,
			OutcomeNotExecuted, nil)
	}
	target := textTargetOf(bound.raw, session.Display)

	if !e.setTextStage(work, op, textStageWrite) {
		return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", e.textCurrentStage(op), textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	if apiErr := e.beginUIDispatchStage(work, "text", "write"); apiErr != nil {
		if apiErr.Code == protocol.CodeStopped {
			return e.textFailureOutcome(op), nil, e.textError(op, 409, protocol.CodeStopped,
				"输入未派发：原工作已撤销", textStageWrite, textReasonAuthorizationRevoked,
				e.textFailureOutcome(op), nil)
		}
		return e.textFailureOutcome(op), nil, e.textError(op, 503, protocol.CodeDeviceUnavailable,
			"输入未派发", textStageWrite, textReasonDispatchNotSent,
			e.textFailureOutcome(op), nil)
	}
	result, err := e.dev.SetTextOnDisplay(work.Ctx, target, operation.Text)
	e.recordUIReceipt(work, result.Receipt)
	if result.Receipt == device.DispatchNotDispatched {
		e.commitTextReceipt(work, op, "write", result.Receipt, err)
		return e.textFailureOutcome(op), nil, e.textError(op, 503, protocol.CodeDeviceUnavailable,
			"输入未派发", textStageWrite, textReasonDispatchNotSent,
			e.textFailureOutcome(op), nil)
	}
	if result.Receipt != device.DispatchTerminated {
		e.commitTextReceipt(work, op, "write", result.Receipt, err)
		reason := textReasonReceiptUnknown
		if err != nil && errors.Is(err, device.ErrTextResultInvalid) {
			reason = textReasonRPCResultInvalid
		}
		return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"文字写入结果无法确认", textStageWrite, reason, OutcomeUncertain, nil)
	}
	e.commitTextReceipt(work, op, "write", device.DispatchTerminated, err)
	if err != nil {
		reason := textReasonReceiptUnknown
		if errors.Is(err, device.ErrTextResultInvalid) {
			reason = textReasonRPCResultInvalid
		}
		return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"文字写入结果无法确认", textStageWrite, reason, OutcomeUncertain, nil)
	}
	if result.CandidateCount != 1 {
		// A complete response with zero or multiple candidates proves the fixed
		// helper never called the write primitive for this target. A response
		// that nevertheless claims issuance or a value is contradictory and
		// stays an unknown receipt.
		if result.Issued || result.Accepted || result.Value != nil {
			return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeTextResultUnknown,
				"文字写入结果无法确认", textStageWrite, textReasonRPCResultInvalid,
				OutcomeUncertain, nil)
		}
		e.setTextWriteNotDispatched(work, op)
		count := result.CandidateCount
		return e.textFailureOutcome(op), nil, e.textError(op, 409, protocol.CodeTextTargetUnknown,
			"目标输入对象无法唯一确认", textStageWrite, textReasonNoUniqueTarget,
			e.textFailureOutcome(op), &count)
	}
	if !result.Issued {
		// The decode layer rejects this contradiction; keep the same unknown
		// receipt classification if a different adapter ever produces it.
		return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"文字写入结果无法确认", textStageWrite, textReasonRPCResultInvalid,
			OutcomeUncertain, nil)
	}
	if !e.noteTextWriteDispatched(work, op) {
		return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", e.textCurrentStage(op), textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	count := 1
	if result.Value == nil {
		return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"文字写入结果无法确认", textStageVerify, textReasonReadbackUnavailable,
			OutcomeUncertain, &count)
	}
	if *result.Value != operation.Text {
		return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeTextResultUnknown,
			"目标输入框的完整内容与请求不一致", textStageVerify, textReasonReadbackMismatch,
			OutcomeUncertain, &count)
	}
	if !e.commitTextWrite(work, op) {
		return OutcomeUncertain, nil, e.textError(op, 409, protocol.CodeStopped,
			"原工作已撤销", e.textCurrentStage(op), textReasonAuthorizationRevoked,
			OutcomeUncertain, nil)
	}
	if !autoSubmit {
		return OutcomeExecuted, nil, nil
	}
	if operation.IsSubmitTap() {
		return e.dispatchTextSubmitTap(work, op, session, obs, submit)
	}
	return e.dispatchTextEnterSubmit(work, op, session, obs, bound, operation.Text)
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
	return textBinding{raw: raw, mode: textNear, capture: &afterTap}, nil
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
				"focus":  textDispatchNotAttempted,
				"write":  textDispatchNotAttempted,
				"submit": textDispatchNotAttempted,
			},
		},
	}
}

// decorateTextRevalidateError attaches the text target/focus detail group only
// to failures that are explicitly about this text target or its revocation.
// Public foreground, display and hierarchy errors keep their own contract.
func (e *Engine) decorateTextRevalidateError(op *operationState, apiErr *protocol.APIError) {
	if apiErr == nil {
		return
	}
	var reason string
	switch apiErr.Code {
	case protocol.CodeTargetChanged:
		reason = textReasonTargetUnconfirmed
	case protocol.CodeStopped:
		if apiErr.Details != nil {
			return
		}
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
	focus, write, submit := textStageSnapshotLocked(op)
	e.mu.Unlock()
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
	// capture is the complete pre-write frame that owns raw. The write-after
	// rebind needs both frames to prove anchor uniqueness; a raw pointer alone
	// cannot answer how many same-anchor fields the pre-write frame had.
	capture *device.Capture
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
			anchor: anchor, anchorKind: kind, capture: fresh}, true
	case len(exact) > 1:
		return textBinding{}, false
	case len(near) == 1:
		return textBinding{raw: near[0], mode: textNear, capture: fresh}, true
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
		anchor: anchor, anchorKind: kind, capture: fresh}, true
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
	if capture == nil || index < 0 || index >= len(capture.Nodes) {
		return nil
	}
	var lineage []rawIdentity
	visited := make(map[int]struct{})
	visited[index] = struct{}{}
	for parent := capture.Nodes[index].Parent; parent >= 0 && parent < len(capture.Nodes); parent = capture.Nodes[parent].Parent {
		if _, seen := visited[parent]; seen {
			break
		}
		visited[parent] = struct{}{}
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
