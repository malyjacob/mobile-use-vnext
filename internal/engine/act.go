package engine

import (
	"context"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Act performs one strict action branch against the current ready observation.
// Static rejection happens before claiming; after claiming the old observation
// is never returned to ready, even when no input was dispatched.
func (e *Engine) Act(ctx context.Context, agent, p string, req *protocol.ActRequest) (*protocol.Result, *protocol.APIError) {
	op, duplicate, apiErr := e.registerOperation(p, agent, "act")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		return duplicate, nil
	}

	e.mu.Lock()
	staticErr := e.staticActCheckLocked(agent, req.ObservationID, &req.Operation, time.Now())
	staticObs := e.observations[req.ObservationID]
	e.mu.Unlock()
	if staticErr == nil {
		staticErr = staticActContentCheck(staticObs, &req.Operation)
	}
	if staticErr != nil {
		return e.finishWithError(op, "admission", staticErr), nil
	}

	work, busy := e.acquireWork(op, WorkAct)
	if busy != nil {
		return e.finishWithError(op, "admission", busy), nil
	}
	defer e.releaseWork(work)

	e.mu.Lock()
	session := e.session
	obs := e.observations[req.ObservationID]
	claimErr := e.claimObservationLocked(agent, work, obs, session, time.Now())
	if claimErr != nil {
		e.mu.Unlock()
		return e.finishWithError(op, "claim", claimErr), nil
	}
	e.touchSessionLocked(time.Now())
	e.mu.Unlock()

	e.setOperationPhase(op, "revalidate")
	fresh, raw, revalidateErr := e.revalidateActTarget(work, obs, &req.Operation)
	if revalidateErr != nil {
		revalidateErr.Outcome = OutcomeNotExecuted
		revalidateErr.Next = "observe"
		return e.finishWithError(op, "revalidate", revalidateErr), nil
	}

	e.setOperationPhase(op, "dispatch")
	outcome, dispatchErr := e.dispatchAct(work, obs, &req.Operation, fresh, raw)
	if outcome == OutcomeUncertain {
		e.markDispatchUnknown(work)
		result := newResult(op)
		result.Outcome = OutcomeUncertain
		result.Error = dispatchErr.Public()
		result.Next = "status"
		e.finishOperation(op, OutcomeUncertain, "dispatch", dispatchErr)
		e.addSessionIfEnded(result)
		return result, nil
	}
	if outcome == OutcomeNotExecuted {
		dispatchErr.Outcome = OutcomeNotExecuted
		dispatchErr.Next = "status"
		return e.finishWithError(op, "dispatch", dispatchErr), nil
	}

	e.clearDispatchUnknown(work)
	e.setOperationPhase(op, "post_observation")
	if e.workRevoked(work) {
		result := newResult(op)
		result.Outcome = OutcomeExecuted
		result.Next = "status"
		e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	session = e.sessionForWork(work)
	if session == nil {
		result := newResult(op)
		result.Outcome = OutcomeExecuted
		result.Next = "status"
		e.finishOperation(op, OutcomeExecuted, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	public, _, postErr := e.captureAndPublish(work.Ctx, work, session, false, req.Image, "act")
	result := newResult(op)
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
		result.Next = "observe"
		e.finishOperation(op, OutcomeExecuted, "post_observation_failed", nil)
		return result, nil
	}
	result.Outcome = OutcomeExecuted
	result.Observation = public
	e.finishOperation(op, OutcomeExecuted, "finished", nil)
	return result, nil
}

func (e *Engine) addSessionIfEnded(result *protocol.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != nil {
		result.IncludeSession = true
		result.Session = e.sessionViewLocked(e.session)
	}
}

// staticActCheckLocked validates the operation against the immutable observation
// content. It performs no claim and no device I/O.
func (e *Engine) staticActCheckLocked(agent, observationID string, operation *protocol.Operation, now time.Time) *protocol.APIError {
	session := e.session
	if session == nil || session.State != SessionActive {
		return protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束").WithNext("end")
	}
	if !sessionBeforeDeadline(session, now) {
		return protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期").WithNext("end")
	}
	obs := e.observations[observationID]
	if obs == nil || obs.Agent != agent || obs.SessionEpoch != session.Epoch {
		return protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
	}
	if e.current != obs {
		return protocol.NewError(409, protocol.CodeObservationRetired, "观察已被替换").WithNext("observe")
	}
	switch obs.Status {
	case ObservationReady:
	case ObservationClaimed:
		return protocol.NewError(409, protocol.CodeObservationClaimed, "观察已被消费").WithNext("observe")
	case ObservationExpired:
		return protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期").WithNext("observe")
	default:
		return protocol.NewError(409, protocol.CodeObservationRetired, "观察已退役").WithNext("observe")
	}
	if now.After(obs.ActionableUntil) {
		obs.Status = ObservationExpired
		return protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期").WithNext("observe")
	}
	return nil
}

// The projection is immutable; all node traversal happens outside e.mu.
func staticActContentCheck(obs *observationState, operation *protocol.Operation) *protocol.APIError {
	screen := obs.Screen
	switch operation.Type {
	case "tap", "long_press", "text":
		if operation.Target == nil {
			return protocol.Invalid("操作缺少 target")
		}
		if operation.Target.UseRef {
			node := findPublicNode(obs.Projection.Tree.Root, operation.Target.Ref)
			if node == nil {
				return protocol.NewError(409, protocol.CodeNodeNotFound, "目标节点不存在")
			}
			if node.Role == protocol.RoleRoot || node.Context || node.Bounds == nil {
				return protocol.NewError(422, protocol.CodeNodeUnsupported, "该节点不能作为操作目标")
			}
			if node.Enabled != nil && !*node.Enabled {
				return protocol.NewError(409, protocol.CodeNodeDisabled, "目标节点已禁用")
			}
			switch operation.Type {
			case "tap":
				if !hasAction(node, protocol.ActionTap) {
					return protocol.NewError(422, protocol.CodeNodeUnsupported, "目标节点不支持点击")
				}
			case "long_press":
				if !hasAction(node, protocol.ActionLongPress) {
					return protocol.NewError(422, protocol.CodeNodeUnsupported, "目标节点不支持长按")
				}
			case "text":
				if node.Role != protocol.RoleInput || !hasAction(node, protocol.ActionText) {
					return protocol.NewError(422, protocol.CodeTextTargetUnknown,
						"文字操作需要可确认的真实输入对象")
				}
				if node.Protected == nil || *node.Protected {
					return protocol.NewError(422, protocol.CodeTextTargetUnknown,
						"无法确认该输入对象不受保护")
				}
			}
		} else if operation.Type != "text" {
			if !inScreen(operation.Target.X, operation.Target.Y, screen) {
				return protocol.NewError(422, protocol.CodeInvalidCoordinates, "坐标超出目标屏幕")
			}
		} else if !inScreen(operation.Target.X, operation.Target.Y, screen) {
			return protocol.NewError(422, protocol.CodeInvalidCoordinates, "坐标超出目标屏幕")
		}
	case "swipe":
		if operation.From == nil || operation.To == nil {
			return protocol.Invalid("swipe 缺少起终点")
		}
		if !inScreen(operation.From.X, operation.From.Y, screen) ||
			!inScreen(operation.To.X, operation.To.Y, screen) {
			return protocol.NewError(422, protocol.CodeInvalidCoordinates, "坐标超出目标屏幕")
		}
	case "key":
		if screen.Rotation < 0 {
			return protocol.Invalid("屏幕信息无效")
		}
		if screen.ID != 0 && (operation.Key == "HOME" || operation.Key == "APP_SWITCH") {
			return protocol.NewError(422, protocol.CodeInvalidKey,
				"HOME/APP_SWITCH 需要共享主屏")
		}
	}
	return nil
}

// claimObservationLocked performs the one-way ready->claimed transition and
// processes the reading acknowledgement of a full candidate.
func (e *Engine) claimObservationLocked(agent string, work *workState, obs *observationState,
	session *sessionState, now time.Time) *protocol.APIError {
	if session == nil || session.State != SessionActive || e.session != session {
		return protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束").WithNext("end")
	}
	if !e.workAuthorizedLocked(work, now) {
		return protocol.NewError(409, protocol.CodeStopped, "原会话已撤销").WithNext("end")
	}
	if obs == nil || obs.Agent != agent || obs.SessionEpoch != session.Epoch {
		return protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
	}
	if e.current != obs || obs.Status != ObservationReady {
		return protocol.NewError(409, protocol.CodeObservationRetired, "观察已被替换").WithNext("observe")
	}
	if now.After(obs.ActionableUntil) {
		obs.Status = ObservationExpired
		return protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期").WithNext("observe")
	}
	obs.Status = ObservationClaimed
	obs.ClaimedBy = work.OperationID
	work.ClaimedO = obs.ID
	if obs.Full {
		e.confirmBaselineLocked(obs)
	}
	return nil
}

// revalidateActTarget re-reads the live display, proves the foreground still
// belongs to the observed window, and maps the public ref to a unique fresh
// source node before any input is dispatched.
func (e *Engine) revalidateActTarget(work *workState, obs *observationState,
	operation *protocol.Operation) (*device.Capture, *device.RawNode, *protocol.APIError) {
	ctx := work.Ctx
	if e.workRevoked(work) {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	session := e.sessionForWork(work)
	if session == nil {
		return nil, nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	state, err := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if err != nil || !state.Exists {
		return nil, nil, protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")
	}
	capture, err := e.dev.Capture(ctx, session.Display)
	if err != nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "实时目标重验失败")
	}
	if err := foregroundMatches(obs.Foreground, capture.Foreground); err != nil {
		return nil, nil, err
	}
	if operation.Target == nil || !operation.Target.UseRef {
		return &capture, nil, nil
	}
	raw := sourceNode(obs, operation.Target.Ref)
	if raw == nil {
		return nil, nil, protocol.NewError(409, protocol.CodeNodeNotFound, "目标节点来源不可用")
	}
	fresh, ok := matchFreshNode(&capture, obs.Projection.Capture, raw)
	if !ok {
		return nil, nil, protocol.NewError(409, protocol.CodeTargetChanged,
			"目标元素已变化，请重新观察").WithNext("observe")
	}
	return &capture, fresh, nil
}

func foregroundMatches(observed, fresh device.Foreground) *protocol.APIError {
	switch observed.State {
	case device.ForegroundPresent:
		if fresh.State != device.ForegroundPresent || fresh.Package != observed.Package {
			return protocol.NewError(409, protocol.CodeForegroundChanged,
				"前台窗口已变化，请重新观察").WithNext("observe")
		}
		if observed.TaskID != nil && fresh.TaskID != nil && *observed.TaskID != *fresh.TaskID {
			return protocol.NewError(409, protocol.CodeForegroundChanged,
				"前台任务已变化，请重新观察").WithNext("observe")
		}
		return nil
	case device.ForegroundNone:
		if fresh.State != device.ForegroundNone {
			return protocol.NewError(409, protocol.CodeForegroundChanged,
				"前台窗口已变化，请重新观察").WithNext("observe")
		}
		return nil
	default:
		return protocol.NewError(409, protocol.CodeForegroundUnknown,
			"无法确认目标窗口归属，请重新观察").WithNext("observe")
	}
}

// dispatchAct executes exactly one input branch and reports whether the device
// accepted it. Anything after the first dispatched primitive that cannot be
// confirmed is uncertain; nothing is replayed.
func (e *Engine) dispatchAct(work *workState, obs *observationState,
	operation *protocol.Operation, fresh *device.Capture, raw *device.RawNode) (string, *protocol.APIError) {
	session := e.sessionForWork(work)
	if session == nil || e.workRevoked(work) {
		return OutcomeNotExecuted, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	displayID := session.Display.ID
	if operation.Type == "text" {
		return e.dispatchText(work, session, obs, operation, fresh, raw)
	}
	x, y := 0, 0
	if operation.Target != nil {
		x, y = operation.Target.X, operation.Target.Y
	}
	if operation.Target != nil && operation.Target.UseRef {
		if raw == nil || raw.Bounds.Empty() {
			return OutcomeNotExecuted, protocol.NewError(409, protocol.CodeTargetChanged,
				"目标元素没有可用几何").WithNext("observe")
		}
		x, y = raw.Bounds.Center()
	}
	if !e.beginDispatch(work) {
		return OutcomeNotExecuted, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	switch operation.Type {
	case "key":
		if err := e.dev.Key(work.Ctx, displayID, operation.Key); err != nil {
			return OutcomeUncertain, actionUncertain()
		}
	case "tap":
		if err := e.dev.Tap(work.Ctx, displayID, x, y); err != nil {
			return OutcomeUncertain, actionUncertain()
		}
	case "long_press":
		if err := e.dev.LongPress(work.Ctx, displayID, x, y); err != nil {
			return OutcomeUncertain, actionUncertain()
		}
	case "swipe":
		if err := e.dev.Swipe(work.Ctx, displayID, operation.From.X, operation.From.Y,
			operation.To.X, operation.To.Y); err != nil {
			return OutcomeUncertain, actionUncertain()
		}
	default:
		return OutcomeNotExecuted, protocol.Invalid("不支持的操作")
	}
	if !e.confirmDispatch(work) {
		return OutcomeUncertain, actionUncertain()
	}
	return OutcomeExecuted, nil
}

// dispatchText performs the focus tap, display-bound set and independent live
// readback. The set is never replayed and text is never rolled back.
func (e *Engine) dispatchText(work *workState, session *sessionState, obs *observationState,
	operation *protocol.Operation, fresh *device.Capture, raw *device.RawNode) (string, *protocol.APIError) {
	displayID := session.Display.ID
	tapX, tapY := operation.Target.X, operation.Target.Y
	if operation.Target.UseRef {
		if raw == nil || raw.Bounds.Empty() {
			return OutcomeNotExecuted, protocol.NewError(409, protocol.CodeTargetChanged,
				"目标元素没有可用几何").WithNext("observe")
		}
		tapX, tapY = raw.Bounds.Center()
	}
	if !e.beginDispatch(work) {
		return OutcomeNotExecuted, protocol.NewError(409, protocol.CodeStopped, "会话已撤销")
	}
	if err := e.dev.Tap(work.Ctx, displayID, tapX, tapY); err != nil {
		return OutcomeUncertain, actionUncertain()
	}
	// The tap is a dispatched input side effect: from here every unknown is
	// uncertain and the original observation is not returned.
	if e.workRevoked(work) {
		return OutcomeUncertain, actionUncertain()
	}
	afterTap, err := e.dev.Capture(work.Ctx, session.Display)
	if err != nil {
		return OutcomeUncertain, textTargetUncertain()
	}
	bound, ok := bindFocusedText(&afterTap, raw, tapX, tapY)
	if !ok {
		return OutcomeUncertain, textTargetUncertain()
	}
	target := textTargetOf(bound, session.Display)
	current, err := e.dev.GetTextOnDisplay(work.Ctx, target)
	if err != nil || current.CandidateCount != 1 {
		return OutcomeUncertain, textTargetUncertain()
	}
	if !e.beginDispatch(work) {
		return OutcomeUncertain, actionUncertain()
	}
	result, err := e.dev.SetTextOnDisplay(work.Ctx, target, operation.Text)
	if err != nil {
		return OutcomeUncertain, textResultUncertain()
	}
	if result.CandidateCount != 1 || !result.Issued || !result.Accepted {
		return OutcomeUncertain, textTargetUncertain()
	}
	delays := []time.Duration{0, 100 * time.Millisecond, 250 * time.Millisecond,
		500 * time.Millisecond, 1000 * time.Millisecond}
	for _, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-work.Ctx.Done():
				timer.Stop()
				return OutcomeUncertain, actionUncertain()
			}
			timer.Stop()
		}
		if e.workRevoked(work) {
			return OutcomeUncertain, actionUncertain()
		}
		readCapture, captureErr := e.dev.Capture(work.Ctx, session.Display)
		if captureErr != nil {
			return OutcomeUncertain, textResultUncertain()
		}
		rebound, bound := bindFocusedText(&readCapture, bound, tapX, tapY)
		if !bound {
			return OutcomeUncertain, textResultUncertain()
		}
		readTarget := textTargetOf(rebound, session.Display)
		readback, readErr := e.dev.GetTextOnDisplay(work.Ctx, readTarget)
		if readErr != nil {
			return OutcomeUncertain, textResultUncertain()
		}
		if readback.CandidateCount != 1 {
			return OutcomeUncertain, textTargetUncertain()
		}
		if readback.Value != nil && *readback.Value == operation.Text {
			if !e.confirmDispatch(work) {
				return OutcomeUncertain, actionUncertain()
			}
			return OutcomeExecuted, nil
		}
	}
	return OutcomeUncertain, textResultUncertain()
}

func actionUncertain() *protocol.APIError {
	return &protocol.APIError{
		Code: protocol.CodeActionUnknown, Message: "输入已派发，但完成结果无法确认",
		HTTPStatus: 504, Outcome: OutcomeUncertain, Next: "status",
	}
}

func textTargetUncertain() *protocol.APIError {
	return &protocol.APIError{
		Code: protocol.CodeTextTargetUnknown, Message: "无法唯一绑定目标输入对象",
		HTTPStatus: 409, Outcome: OutcomeUncertain, Next: "status",
	}
}

func textResultUncertain() *protocol.APIError {
	return &protocol.APIError{
		Code: protocol.CodeTextResultUnknown, Message: "文字写入结果无法确认",
		HTTPStatus: 409, Outcome: OutcomeUncertain, Next: "status",
	}
}

// bindFocusedText finds exactly one focused, enabled, non-password editable
// node on the target display. A structural match wins; otherwise the field must
// cover the tap point and at least 80% of the smaller rectangle.
func bindFocusedText(capture *device.Capture, want *device.RawNode, tapX, tapY int) (*device.RawNode, bool) {
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

// rawIdentity is the complete source identity used for live revalidation; it
// deliberately excludes the snapshot index.
type rawIdentity struct {
	Package, Class, ResourceID, Text, Description string
	Bounds                                        device.Rect
	Clickable, LongClickable, Scrollable          bool
	Editable, Password                            bool
	HasEnabled, Enabled                           bool
	HasChecked, Checked                           bool
	HasSelected, Selected                         bool
	HasFocused, Focused                           bool
}

func identityOf(node *device.RawNode) rawIdentity {
	return rawIdentity{
		Package: node.Package, Class: node.ClassName, ResourceID: node.ResourceID,
		Text: node.Text, Description: node.Description, Bounds: node.Bounds,
		Clickable: node.Clickable, LongClickable: node.LongClickable,
		Scrollable: node.Scrollable, Editable: node.Editable, Password: node.Password,
		HasEnabled: node.HasEnabled, Enabled: node.Enabled,
		HasChecked: node.HasChecked, Checked: node.Checked,
		HasSelected: node.HasSelected, Selected: node.Selected,
		HasFocused: node.HasFocused, Focused: node.Focused,
	}
}

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
