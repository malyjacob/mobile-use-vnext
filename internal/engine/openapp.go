package engine

import (
	"context"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Owned window registry values. A virtual session owns every task on its own
// display; primary sessions never register user windows.
const (
	TaskOwned  = "owned"
	TaskShared = "shared"
)

// taskEntry is one session-owned window. The registry is a cache: every
// decision reconciles it against a fresh task read by task id.
type taskEntry struct {
	TaskID     int
	Package    string
	DisplayID  int
	Generation string
	Ownership  string
	Proven     bool
	CreatedAt  time.Time
}

// OpenApp starts or reuses one application window on the session display. It
// consumes the same current ready observation as act and returns one fresh
// full observation.
func (e *Engine) OpenApp(ctx context.Context, agent, p string, req *protocol.OpenAppRequest) (finalResult *protocol.Result, finalErr *protocol.APIError) {
	op, duplicate, apiErr := e.registerOperation(p, agent, "open_app")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		return duplicate, nil
	}
	defer func() { e.applyFrozenResult(op, &finalResult) }()
	e.setOperationReference(op, protocol.RecoveryReferenceO)
	if req == nil || req.App == "" {
		return e.finishWithError(op, "admission", protocol.Invalid("open_app 需要 app 包名")), nil
	}
	now := time.Now()
	e.mu.Lock()
	obs, session, staticErr := e.openAppStaticCheckLocked(agent, req.ObservationID, now)
	if staticErr == nil && session.Mode == "virtual" && req.App == e.cfg.ControlPackage {
		staticErr = protocol.NewError(409, protocol.CodeControlUI,
			"控制界面不能在虚拟屏上打开")
	}
	e.mu.Unlock()
	if staticErr != nil {
		return e.finishWithError(op, "admission", staticErr), nil
	}

	work, busy := e.acquireWork(op, WorkOpenApp)
	if busy != nil {
		return e.finishWithError(op, "admission", busy), nil
	}
	defer e.releaseWork(work)
	_ = obs

	// Read-only evidence is gathered before the claim so an unknown package or
	// an unreadable task list does not consume the observation.
	component, found, resolveErr := e.dev.ResolveLauncher(work.Ctx, req.App)
	if resolveErr != nil {
		if e.detachedWorkRevoked(work) {
			return e.finishWithError(op, "admission",
				protocol.NewError(409, protocol.CodeStopped, "应用启动已撤销")), nil
		}
		return e.finishWithError(op, "admission",
			protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法确认应用启动入口")), nil
	}
	if !found {
		return e.finishWithError(op, "admission",
			protocol.NewError(409, protocol.CodeAppNotFound, "手机上找不到可启动的该应用")), nil
	}
	preTasks, tasksKnown := e.dev.TasksOnDisplay(work.Ctx, session.Display.ID)
	if !tasksKnown {
		return e.finishWithError(op, "admission",
			protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取目标屏幕任务")), nil
	}

	e.mu.Lock()
	if e.work != work || work.Revoked {
		e.mu.Unlock()
		return e.finishWithError(op, "admission",
			protocol.NewError(409, protocol.CodeStopped, "应用启动已撤销")), nil
	}
	claimErr := e.claimObservationLocked(op, agent, work, e.observations[req.ObservationID],
		e.session, time.Now())
	e.mu.Unlock()
	if claimErr != nil {
		return e.finishWithError(op, "claim", claimErr), nil
	}

	probeCtx, cancel := context.WithTimeout(work.Ctx, 5*time.Second)
	displayState, probeErr := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if probeErr != nil || !displayState.Exists {
		return e.finishWithError(op, "revalidate",
			protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")), nil
	}
	if e.detachedWorkRevoked(work) {
		return e.finishWithError(op, "revalidate",
			protocol.NewError(409, protocol.CodeStopped, "应用启动已撤销")), nil
	}
	if session.Mode == "primary" {
		return e.openAppPrimary(work, op, session, req.App, component, preTasks)
	}
	return e.openAppVirtual(work, op, session, req.App, component, preTasks)
}

// openAppStaticCheckLocked validates the session and the ready observation
// without claiming or dispatching anything.
func (e *Engine) openAppStaticCheckLocked(agent, observationID string, now time.Time) (*observationState, *sessionState, *protocol.APIError) {
	session := e.session
	if session == nil || session.State != SessionActive {
		return nil, nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	if !sessionBeforeDeadline(session, now) {
		return nil, nil, protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期")
	}
	obs := e.observations[observationID]
	if obs == nil || obs.Agent != agent || obs.SessionEpoch != session.Epoch {
		return nil, nil, protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
	}
	if e.current != obs {
		return nil, nil, protocol.NewError(409, protocol.CodeObservationRetired, "观察已被替换")
	}
	switch obs.Status {
	case ObservationReady:
	case ObservationClaimed:
		return nil, nil, protocol.NewError(409, protocol.CodeObservationClaimed, "观察已被消费")
	case ObservationExpired:
		return nil, nil, protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期")
	default:
		return nil, nil, protocol.NewError(409, protocol.CodeObservationRetired, "观察已退役")
	}
	if now.After(obs.ActionableUntil) {
		obs.Status = ObservationExpired
		return nil, nil, protocol.NewError(409, protocol.CodeObservationExpired, "观察已过期")
	}
	return obs, session, nil
}

// openAppVirtual reuses an owned live window or creates a new one on the
// session display. A reused window is focused and re-verified before it is
// reported as owned.
func (e *Engine) openAppVirtual(work *workState, op *operationState, session *sessionState,
	app, component string, preTasks []device.Task) (*protocol.Result, *protocol.APIError) {
	if task := reusableTask(preTasks, app, session.Display.ID); task != nil {
		if dispatchErr := e.beginUIDispatch(work, "focus"); dispatchErr != nil {
			return e.finishWithError(op, "dispatch",
				dispatchErr), nil
		}
		receipt, focusErr := e.dev.FocusTask(work.Ctx, task.TaskID)
		e.recordUIReceipt(work, receipt)
		if receipt == device.DispatchNotDispatched {
			return e.finishOpenAppNotDispatched(work, op), nil
		}
		if focusErr != nil || receipt != device.DispatchTerminated {
			e.markDispatchUnknown(work)
			return e.finishOpenAppUncertain(op, protocol.CodeTaskFocus,
				"已请求复用窗口，但无法确认置前", nil)
		}
		if !e.verifyForeground(work, session.Display.ID, app, task.TaskID) {
			e.markDispatchUnknown(work)
			return e.finishOpenAppUncertain(op, protocol.CodeTaskFocus,
				"已请求复用窗口，但无法确认置前", nil)
		}
		e.clearDispatchUnknown(work)
		e.rememberTask(session, *task, true)
		return e.finishOpenAppSuccess(work, op, session, app, task.TaskID,
			true, true, OutcomeExecuted, nil)
	}

	if dispatchErr := e.beginUIDispatch(work, "launch"); dispatchErr != nil {
		return e.finishWithError(op, "dispatch",
			dispatchErr), nil
	}
	launch, launchErr := e.dev.LaunchApp(work.Ctx, session.Display.ID, component)
	e.recordUIReceipt(work, launch.Receipt)
	if launch.Receipt == device.DispatchNotDispatched {
		return e.finishOpenAppNotDispatched(work, op), nil
	}
	if launchErr != nil || launch.State == device.LaunchUnknown || launch.Receipt == device.DispatchUnknown {
		e.markDispatchUnknown(work)
		return e.finishOpenAppUncertain(op, protocol.CodeAppLaunchUnknown,
			"已派发应用启动，但无法确认结果", nil)
	}
	if launch.State == device.LaunchRejected || launch.State == device.LaunchNotStarted {
		e.clearDispatchUnknown(work)
		return e.finishWithError(op, "dispatch",
			protocol.NewError(503, protocol.CodeAppLaunchFailed, "应用启动被设备拒绝")), nil
	}
	task, moved, ok := e.verifyLaunchedTask(work, session, app, preTasks)
	if !ok {
		e.markDispatchUnknown(work)
		return e.finishOpenAppUncertain(op, protocol.CodeAppLaunchUnknown,
			"已派发应用启动，但无法确认任务归属", nil)
	}
	if moved {
		e.clearDispatchUnknown(work)
		return e.finishOpenAppUncertain(op, protocol.CodeAppMoved,
			"应用任务未落在目标屏幕", nil)
	}
	e.clearDispatchUnknown(work)
	e.rememberTask(session, *task, true)
	return e.finishOpenAppSuccess(work, op, session, app, task.TaskID,
		false, false, OutcomeExecuted, nil)
}

// openAppPrimary opens an ordinary same-screen window. It never registers or
// removes a user task; the response only carries the correlated task fact.
func (e *Engine) openAppPrimary(work *workState, op *operationState, session *sessionState,
	app, component string, preTasks []device.Task) (*protocol.Result, *protocol.APIError) {
	if task := reusableTask(preTasks, app, 0); task != nil {
		if dispatchErr := e.beginUIDispatch(work, "focus"); dispatchErr != nil {
			return e.finishWithError(op, "dispatch",
				dispatchErr), nil
		}
		receipt, focusErr := e.dev.FocusTask(work.Ctx, task.TaskID)
		e.recordUIReceipt(work, receipt)
		if receipt == device.DispatchNotDispatched {
			return e.finishOpenAppNotDispatched(work, op), nil
		}
		if focusErr != nil || receipt != device.DispatchTerminated {
			e.markDispatchUnknown(work)
			return e.finishOpenAppUncertain(op, protocol.CodeTaskFocus,
				"已请求复用窗口，但无法确认置前", nil)
		}
		if !e.verifyForeground(work, 0, app, task.TaskID) {
			return e.finishOpenAppUncertain(op, protocol.CodeTaskFocus, "已请求复用窗口，但无法确认置前", nil)
		}
		e.clearDispatchUnknown(work)
		return e.finishOpenAppSuccess(work, op, session, app, task.TaskID,
			true, true, OutcomeExecuted, nil)
	}
	if dispatchErr := e.beginUIDispatch(work, "launch"); dispatchErr != nil {
		return e.finishWithError(op, "dispatch",
			dispatchErr), nil
	}
	launch, launchErr := e.dev.LaunchApp(work.Ctx, 0, component)
	e.recordUIReceipt(work, launch.Receipt)
	if launch.Receipt == device.DispatchNotDispatched {
		return e.finishOpenAppNotDispatched(work, op), nil
	}
	if launchErr != nil || launch.State == device.LaunchUnknown || launch.Receipt == device.DispatchUnknown {
		e.markDispatchUnknown(work)
		return e.finishOpenAppUncertain(op, protocol.CodeAppLaunchUnknown,
			"已派发应用启动，但无法确认结果", nil)
	}
	if launch.State == device.LaunchRejected || launch.State == device.LaunchNotStarted {
		e.clearDispatchUnknown(work)
		return e.finishWithError(op, "dispatch",
			protocol.NewError(503, protocol.CodeAppLaunchFailed, "应用启动被设备拒绝")), nil
	}
	task, moved, ok := e.verifyLaunchedTask(work, session, app, preTasks)
	if !ok || moved {
		e.markDispatchUnknown(work)
		code := protocol.CodeAppLaunchUnknown
		if moved {
			code = protocol.CodeAppMoved
		}
		return e.finishOpenAppUncertain(op, code, "已派发应用启动，但无法确认任务归属", nil)
	}
	e.clearDispatchUnknown(work)
	return e.finishOpenAppSuccess(work, op, session, app, task.TaskID,
		false, false, OutcomeExecuted, nil)
}

func ledgerTasksOf(tasks []taskEntry) []LedgerTask {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]LedgerTask, 0, len(tasks))
	for i, task := range tasks {
		if i >= 16 || task.TaskID <= 0 {
			break
		}
		out = append(out, LedgerTask{
			TaskID: task.TaskID, Package: task.Package,
			DisplayID: task.DisplayID, Proven: task.Proven,
		})
	}
	return out
}

func tasksFromLedger(tasks []LedgerTask) []taskEntry {
	out := make([]taskEntry, 0, len(tasks))
	for i, task := range tasks {
		if i >= 16 || task.TaskID <= 0 {
			break
		}
		out = append(out, taskEntry{
			TaskID: task.TaskID, Package: task.Package, DisplayID: task.DisplayID,
			Ownership: TaskOwned, Proven: task.Proven,
		})
	}
	return out
}

// reusableTask returns one live pre-task for the package on the display.
func reusableTask(tasks []device.Task, app string, displayID int) *device.Task {
	for i := range tasks {
		if tasks[i].Package == app && tasks[i].DisplayID == displayID && tasks[i].TaskID > 0 {
			return &tasks[i]
		}
	}
	return nil
}

// verifyForeground polls the target display until the expected task or package
// is foreground, bounded to four short rounds.
func (e *Engine) verifyForeground(work *workState, displayID int, app string, taskID int) bool {
	for _, delay := range []time.Duration{0, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond} {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-work.Ctx.Done():
				timer.Stop()
				return false
			}
			timer.Stop()
		}
		foreground, err := e.dev.Foreground(work.Ctx, displayID)
		if err != nil || foreground.State != device.ForegroundPresent {
			continue
		}
		if foreground.TaskID != nil && *foreground.TaskID == taskID {
			return true
		}
		if foreground.TaskID == nil && foreground.Package == app {
			return true
		}
	}
	return false
}

// verifyLaunchedTask polls the live task list for exactly one new matching
// task. moved reports a unique new task on another display.
func (e *Engine) verifyLaunchedTask(work *workState, session *sessionState,
	app string, preTasks []device.Task) (*device.Task, bool, bool) {
	known := make(map[int]bool, len(preTasks))
	for _, task := range preTasks {
		known[task.TaskID] = true
	}
	var candidate *device.Task
	moved := false
	for _, delay := range []time.Duration{0, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1200 * time.Millisecond} {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-work.Ctx.Done():
				timer.Stop()
				return nil, false, false
			}
			timer.Stop()
		}
		all, err := e.dev.TaskList(work.Ctx)
		if err != nil {
			continue
		}
		var matches []device.Task
		for _, task := range all {
			if task.Package != app || known[task.TaskID] || task.TaskID <= 0 {
				continue
			}
			matches = append(matches, task)
		}
		switch {
		case len(matches) == 1:
			task := matches[0]
			if task.DisplayID == session.Display.ID {
				return &task, false, true
			}
			candidate, moved = &task, true
		case len(matches) > 1:
			return nil, false, false
		}
	}
	if candidate != nil {
		return candidate, moved, true
	}
	return nil, false, false
}

// rememberTask records one proven owned window on the virtual session.
func (e *Engine) rememberTask(session *sessionState, task device.Task, proven bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil || e.session.Epoch != session.Epoch || e.session.State == SessionEnded {
		return
	}
	for i := range e.session.Tasks {
		if e.session.Tasks[i].TaskID == task.TaskID {
			e.session.Tasks[i].DisplayID = task.DisplayID
			e.session.Tasks[i].Proven = e.session.Tasks[i].Proven || proven
			return
		}
	}
	if len(e.session.Tasks) >= 16 {
		return
	}
	e.session.Tasks = append(e.session.Tasks, taskEntry{
		TaskID: task.TaskID, Package: task.Package, DisplayID: task.DisplayID,
		Generation: e.session.Display.Generation, Ownership: TaskOwned,
		Proven: proven, CreatedAt: time.Now(),
	})
	e.persistAsync()
}

// finishOpenAppSuccess commits the action fact and performs one full post
// observation. A post-observation failure never rewrites executed.
func (e *Engine) finishOpenAppSuccess(work *workState, op *operationState, session *sessionState,
	app string, taskID int, reused, preserved bool, outcome string, apiErr *protocol.APIError) (*protocol.Result, *protocol.APIError) {
	e.noteKnownOutcome(op, outcome)
	result := newResult(op)
	result.Outcome = outcome
	result.App = &protocol.AppView{
		Package: app, TaskID: taskID, DisplayID: session.Display.ID,
		Reused: reused, StatePreserved: &preserved,
	}
	if apiErr != nil {
		result.Error = apiErr.Public()
	}
	if e.detachedWorkRevoked(work) {
		result.Recovery = e.finishOperation(op, outcome, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	current := e.sessionForWork(work)
	if current == nil {
		result.Recovery = e.finishOperation(op, outcome, "executed_revoked", nil)
		e.addSessionIfEnded(result)
		return result, nil
	}
	public, _, postErr := e.captureAndPublish(work.Ctx, work, current, true, false, "open_app")
	if postErr != nil {
		result.Observation = &protocol.Observation{
			Status: "failed", Missing: []string{"tree"}, Error: postErr.Public(),
		}
		result.Recovery = e.finishOperation(op, outcome, "post_observation_failed", nil)
		return result, nil
	}
	result.Observation = public
	result.Recovery = e.finishOperation(op, outcome, "finished", nil)
	return result, nil
}

func (e *Engine) finishOpenAppUncertain(op *operationState, code, message string, details map[string]any) (*protocol.Result, *protocol.APIError) {
	apiErr := protocol.NewError(504, code, message)
	if code == protocol.CodeAppMoved || code == protocol.CodeTaskFocus {
		apiErr.HTTPStatus = 409
	}
	apiErr.Outcome = OutcomeUncertain
	for key, value := range details {
		apiErr.WithDetail(key, value)
	}
	result := newResult(op)
	result.Outcome = OutcomeUncertain
	result.Error = apiErr.Public()
	result.Recovery = e.finishOperation(op, OutcomeUncertain, "dispatch", apiErr)
	e.addSessionIfEnded(result)
	return result, nil
}

// A definite non-dispatch is independent of a transport/semantic error.
func (e *Engine) finishOpenAppNotDispatched(work *workState, op *operationState) *protocol.Result {
	code, message := protocol.CodeAppLaunchFailed, "应用启动或置前未派发"
	if e.workRevoked(work) {
		code, message = protocol.CodeStopped, "应用启动已撤销，未派发"
	}
	return e.finishWithError(op, "dispatch", protocol.NewError(protocol.Status(code), code, message))
}
