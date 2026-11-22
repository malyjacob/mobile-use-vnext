package engine

import (
	"context"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Owned window registry values. A virtual session owns the windows it created
// and the primary tasks it explicitly claimed. Everything else is shared.
const (
	TaskOwned    = "owned"
	TaskBorrowed = "borrowed"
	TaskShared   = "shared"
)

// taskEntry is one session-managed window. The registry is a cache: every
// decision reconciles it against a fresh task read by task id.
type taskEntry struct {
	TaskID     int
	RootID     int
	Package    string
	DisplayID  int
	Generation string
	Ownership  string
	// OriginDisplay is the original-ownership display of a borrowed task.
	OriginDisplay *int
	Proven        bool
	CreatedAt     time.Time
}

// appOpen starts or reuses one application window on the session display. It
// consumes the same current ready observation as act and returns one fresh full
// observation. A primary open never registers a user task.
func (e *Engine) appOpen(ctx context.Context, op *operationState, req *protocol.AppRequest) (*protocol.Result, *protocol.APIError) {
	now := time.Now()
	e.mu.Lock()
	_, session, staticErr := e.appStaticCheckLocked(op.Agent, req.Reference, now)
	if staticErr == nil && session.Mode == "virtual" && req.Target.App == e.cfg.ControlPackage {
		staticErr = protocol.NewError(409, protocol.CodeControlUI,
			"控制界面不能在虚拟屏上打开")
	}
	e.mu.Unlock()
	if staticErr != nil {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind), staticErr)
	}

	work, busy := e.acquireWork(op, WorkAppAction)
	if busy != nil {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind), busy)
	}
	defer e.releaseWork(work)

	// The pre-launch baseline is one whole-device task read: reuse is decided
	// from the target-display subset, while the complete snapshot is what proves
	// which task ids are new afterwards. A same-package window on the primary
	// display is therefore never mistaken for this launch. Reuse is still
	// decided before any launcher resolution: an unrelated broken launcher entry
	// must not block an existing window.
	preAllTasks, preErr := e.dev.TaskList(work.Ctx)
	if preErr != nil {
		return e.finishAppError(op, "admission",
			&protocol.AppView{Kind: req.Kind, Package: req.Target.App},
			protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取目标屏幕任务"))
	}
	preDisplayTasks := tasksOnDisplay(preAllTasks, session.Display.ID)
	foreground, _ := e.dev.Foreground(work.Ctx, session.Display.ID)
	reuse := selectReusableTask(preDisplayTasks, req.Target.App, session.Display.ID, foreground)

	component := ""
	if reuse == nil {
		resolved, found, resolveErr := e.dev.ResolveLauncher(work.Ctx, req.Target.App)
		if resolveErr != nil {
			if e.detachedWorkRevoked(work) {
				return e.finishAppError(op, "admission",
					&protocol.AppView{Kind: req.Kind, Package: req.Target.App},
					protocol.NewError(409, protocol.CodeStopped, "应用启动已撤销"))
			}
			return e.finishAppError(op, "admission",
				&protocol.AppView{Kind: req.Kind, Package: req.Target.App},
				protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法确认应用启动入口"))
		}
		if !found {
			return e.finishAppError(op, "admission",
				&protocol.AppView{Kind: req.Kind, Package: req.Target.App},
				protocol.NewError(409, protocol.CodeAppNotFound, "手机上找不到可启动的该应用"))
		}
		component = resolved
	}

	// Reserve the bounded ownership record before the observation is consumed.
	// Reusing an already registered window grows nothing.
	if session.Mode == "virtual" && (reuse == nil || e.isNewSessionTask(session, reuse.TaskID)) {
		e.mu.Lock()
		refusal := e.appCapacityRefusalLocked(&LedgerTask{
			TaskID: 2147483647, Package: req.Target.App, DisplayID: session.Display.ID,
			RootTaskID: 2147483647, Ownership: TaskOwned, Proven: true,
		}, nil)
		e.mu.Unlock()
		if refusal != nil {
			return e.finishAppError(op, "admission",
				&protocol.AppView{Kind: req.Kind, Package: req.Target.App}, refusal)
		}
	}

	e.mu.Lock()
	claimErr := e.claimObservationLocked(op, op.Agent, work, e.observations[req.Reference],
		e.session, time.Now())
	e.mu.Unlock()
	if claimErr != nil {
		return e.finishAppError(op, "claim",
			&protocol.AppView{Kind: req.Kind, Package: req.Target.App}, claimErr)
	}

	probeCtx, cancel := context.WithTimeout(work.Ctx, 5*time.Second)
	displayState, probeErr := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if probeErr != nil || !displayState.Exists {
		return e.finishAppError(op, "revalidate",
			&protocol.AppView{Kind: req.Kind, Package: req.Target.App},
			protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用"))
	}
	if e.detachedWorkRevoked(work) {
		return e.finishAppError(op, "revalidate",
			&protocol.AppView{Kind: req.Kind, Package: req.Target.App},
			protocol.NewError(409, protocol.CodeStopped, "应用启动已撤销"))
	}
	if session.Mode == "primary" {
		return e.finishOpenPrimary(work, op, session, req.Target.App, component, reuse, preAllTasks)
	}
	return e.finishOpenVirtual(work, op, session, req.Target.App, component, reuse, preAllTasks)
}

// tasksOnDisplay returns the subset of one task snapshot hosted by a display. It
// is a pure filter so both the pre-launch baseline and the launch attribution
// derive the target-display view from the same fact.
func tasksOnDisplay(tasks []device.Task, displayID int) []device.Task {
	var out []device.Task
	for _, task := range tasks {
		if task.DisplayID == displayID {
			out = append(out, task)
		}
	}
	return out
}

// diffNewPackageTasks splits the tasks of one package that did not exist in the
// pre-launch whole-device snapshot into the target-display set and the tasks
// that landed elsewhere. Known task ids are excluded on every call, so a task
// the system silently moved between displays is never reported as new.
func diffNewPackageTasks(all, preAll []device.Task, app string, displayID int) (onTarget, elsewhere []device.Task) {
	known := make(map[int]bool, len(preAll))
	for _, task := range preAll {
		if task.TaskID > 0 {
			known[task.TaskID] = true
		}
	}
	for _, task := range all {
		if task.TaskID <= 0 || task.Package != app || known[task.TaskID] {
			continue
		}
		if task.DisplayID == displayID {
			onTarget = append(onTarget, task)
		} else {
			elsewhere = append(elsewhere, task)
		}
	}
	return onTarget, elsewhere
}

// isNewSessionTask reports whether this task id is not yet in the registry.
func (e *Engine) isNewSessionTask(session *sessionState, taskID int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessionTaskLocked(e.session, taskID) == nil
}

// selectReusableTask chooses one existing window of the package on the display:
// the frontmost window when the foreground names it, otherwise the lowest task
// id. The rule is deterministic and never used for claim/handoff selection.
func selectReusableTask(tasks []device.Task, app string, displayID int,
	foreground device.Foreground) *device.Task {
	var candidates []device.Task
	for _, task := range tasks {
		if task.Package == app && task.DisplayID == displayID && task.TaskID > 0 {
			candidates = append(candidates, task)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sortTasks(candidates)
	if foreground.State == device.ForegroundPresent && foreground.TaskID != nil {
		for i := range candidates {
			if candidates[i].TaskID == *foreground.TaskID {
				return &candidates[i]
			}
		}
	}
	return &candidates[0]
}

// finishOpenVirtual reuses an owned live window or creates a new one on the
// session display. A reused window is focused and re-verified before it is
// reported as owned.
func (e *Engine) finishOpenVirtual(work *workState, op *operationState, session *sessionState,
	app, component string, reuse *device.Task, preAllTasks []device.Task) (*protocol.Result, *protocol.APIError) {
	if reuse != nil {
		if dispatchErr := e.beginUIDispatch(work, "focus"); dispatchErr != nil {
			return e.finishAppError(op, "dispatch",
				&protocol.AppView{
					Kind: protocol.AppKindOpen, Package: app,
					TaskID: appIntPtr(reuse.TaskID), DisplayID: appIntPtr(session.Display.ID),
					Ownership: TaskOwned,
				}, dispatchErr)
		}
		receipt, focusErr := e.dev.FocusTask(work.Ctx, reuse.TaskID)
		e.recordUIReceipt(work, receipt)
		if receipt == device.DispatchNotDispatched {
			return e.finishOpenNotDispatched(work, op, session, app, reuse.TaskID, true)
		}
		if focusErr != nil || receipt != device.DispatchTerminated {
			e.markDispatchUnknown(work)
			return e.finishAppUncertainWork(work, op, &protocol.AppView{
				Kind: protocol.AppKindOpen, Package: app,
				TaskID: appIntPtr(reuse.TaskID), DisplayID: appIntPtr(session.Display.ID),
				Ownership: TaskOwned, Reused: appBoolPtr(true),
				StatePreserved: nil, HasStatePreserved: true,
			}, protocol.CodeTaskFocus, "已请求复用窗口，但无法确认置前", nil)
		}
		if !e.verifyForeground(work, session.Display.ID, app, reuse.TaskID) {
			e.markDispatchUnknown(work)
			return e.finishAppUncertainWork(work, op, &protocol.AppView{
				Kind: protocol.AppKindOpen, Package: app,
				TaskID: appIntPtr(reuse.TaskID), DisplayID: appIntPtr(session.Display.ID),
				Ownership: TaskOwned, Reused: appBoolPtr(true),
				StatePreserved: nil, HasStatePreserved: true,
			}, protocol.CodeTaskFocus, "已请求复用窗口，但无法确认置前", nil)
		}
		e.clearDispatchUnknown(work)
		e.rememberTask(session, *reuse, true)
		return e.finishAppExecuted(work, op, &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: app,
			TaskID: appIntPtr(reuse.TaskID), DisplayID: appIntPtr(session.Display.ID),
			Ownership: TaskOwned, Reused: appBoolPtr(true),
			StatePreserved:    e.reuseStatePreserved(work, session, reuse),
			HasStatePreserved: true,
		})
	}

	if dispatchErr := e.beginUIDispatch(work, "launch"); dispatchErr != nil {
		return e.finishAppError(op, "dispatch",
			&protocol.AppView{Kind: protocol.AppKindOpen, Package: app}, dispatchErr)
	}
	launch, launchErr := e.dev.LaunchApp(work.Ctx, session.Display.ID, component)
	e.recordUIReceipt(work, launch.Receipt)
	if launch.Receipt == device.DispatchNotDispatched {
		return e.finishOpenNotDispatched(work, op, session, app, 0, false)
	}
	if launchErr != nil || launch.State == device.LaunchUnknown || launch.Receipt == device.DispatchUnknown {
		e.markDispatchUnknown(work)
		return e.finishOpenUncertain(work, op, session, app, preAllTasks, &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: app, Reused: appBoolPtr(false),
			StatePreserved: nil, HasStatePreserved: true,
		}, protocol.CodeAppLaunchUnknown, "已派发应用启动，但无法确认结果")
	}
	if launch.State == device.LaunchRejected || launch.State == device.LaunchNotStarted {
		e.clearDispatchUnknown(work)
		return e.finishAppError(op, "dispatch",
			&protocol.AppView{Kind: protocol.AppKindOpen, Package: app},
			protocol.NewError(503, protocol.CodeAppLaunchFailed, "应用启动被设备拒绝"))
	}
	task, moved, ok := e.verifyLaunchedTask(work, session, app, preAllTasks)
	if !ok {
		e.markDispatchUnknown(work)
		return e.finishOpenUncertain(work, op, session, app, preAllTasks, &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: app, Reused: appBoolPtr(false),
			StatePreserved: nil, HasStatePreserved: true,
		}, protocol.CodeAppLaunchUnknown, "已派发应用启动，但无法确认任务归属")
	}
	if moved {
		e.clearDispatchUnknown(work)
		return e.finishAppUncertainWork(work, op, &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: app,
			TaskID: appIntPtr(task.TaskID), Reused: appBoolPtr(false),
			StatePreserved: nil, HasStatePreserved: true,
		}, protocol.CodeAppMoved, "应用任务未落在目标屏幕", nil)
	}
	e.clearDispatchUnknown(work)
	e.rememberTask(session, *task, true)
	return e.finishAppExecuted(work, op, &protocol.AppView{
		Kind: protocol.AppKindOpen, Package: app,
		TaskID: appIntPtr(task.TaskID), DisplayID: appIntPtr(session.Display.ID),
		Ownership: TaskOwned, Reused: appBoolPtr(false),
		StatePreserved: appBoolPtr(false), HasStatePreserved: true,
	})
}

// finishOpenUncertain closes one virtual launch whose result is unknown. After
// the post observation it reconciles the live task list once, so an already
// landed unique new window still becomes a proven owned fact. The outcome stays
// uncertain: a later read never rewrites a dispatch its call could not confirm.
func (e *Engine) finishOpenUncertain(work *workState, op *operationState, session *sessionState,
	app string, preAllTasks []device.Task, view *protocol.AppView,
	code, message string) (*protocol.Result, *protocol.APIError) {
	return e.finishAppUncertainWorkAfter(work, op, view, code, message, nil, func() {
		e.reconcileOpenVirtualTask(work, session, app, preAllTasks, view)
	})
}

// reconcileOpenVirtualTask adopts the already-proven landing point of one
// unconfirmed virtual launch. Only a unique new same-package task on the target
// display counts as this launch; a known task the system moved between displays
// never does. Ownership is written only after the conditional registration
// succeeded, so a revoked or protected commit fabricates nothing.
func (e *Engine) reconcileOpenVirtualTask(work *workState, session *sessionState,
	app string, preAllTasks []device.Task, view *protocol.AppView) {
	if view == nil || session == nil || session.Mode != "virtual" {
		return
	}
	if e.detachedWorkRevoked(work) || e.sessionForWork(work) == nil {
		return
	}
	all, err := e.dev.TaskList(work.Ctx)
	if err != nil {
		return
	}
	onTarget, elsewhere := diffNewPackageTasks(all, preAllTasks, app, session.Display.ID)
	switch {
	case len(onTarget) == 1:
		task := onTarget[0]
		view.TaskID = appIntPtr(task.TaskID)
		view.DisplayID = appIntPtr(session.Display.ID)
		view.Reused = appBoolPtr(false)
		view.StatePreserved = appBoolPtr(false)
		view.HasStatePreserved = true
		if e.rememberOwnedTask(work, session, task) {
			view.Ownership = TaskOwned
		}
	case len(onTarget) == 0 && len(elsewhere) == 1 && view.TaskID == nil:
		// A task that demonstrably landed elsewhere is only an identity; it is
		// neither an owned window of this session nor a proven target display.
		view.TaskID = appIntPtr(elsewhere[0].TaskID)
	}
}

// rememberOwnedTask conditionally registers one proven new window as owned. The
// state lock only checks memory: the work, the original session epoch, the
// virtual mode and the transfer protection must all still hold, and the bounded
// registry must have room. It reports whether the ownership record now exists.
func (e *Engine) rememberOwnedTask(work *workState, session *sessionState, task device.Task) bool {
	if session == nil || task.TaskID <= 0 {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil || e.session.Epoch != session.Epoch ||
		e.session.State == SessionEnded || e.session.Mode != "virtual" {
		return false
	}
	if e.work != work || work == nil || work.Revoked || e.shuttingDown || work.Ctx.Err() != nil {
		return false
	}
	if e.transferProtectsLocked(task.TaskID) {
		return false
	}
	for i := range e.session.Tasks {
		if e.session.Tasks[i].TaskID != task.TaskID {
			continue
		}
		if e.session.Tasks[i].Ownership != TaskOwned {
			return false
		}
		e.session.Tasks[i].DisplayID = task.DisplayID
		e.session.Tasks[i].RootID = task.RootID
		e.session.Tasks[i].Proven = true
		return true
	}
	if len(e.session.Tasks) >= 16 {
		return false
	}
	e.session.Tasks = append(e.session.Tasks, taskEntry{
		TaskID: task.TaskID, RootID: task.RootID, Package: task.Package,
		DisplayID: task.DisplayID, Generation: e.session.Display.Generation,
		Ownership: TaskOwned, Proven: true, CreatedAt: time.Now(),
	})
	e.persistAsync()
	return true
}

// finishOpenPrimary opens an ordinary same-screen window. It never registers or
// removes a user task; the response only carries the verified task fact.
func (e *Engine) finishOpenPrimary(work *workState, op *operationState, session *sessionState,
	app, component string, reuse *device.Task, preAllTasks []device.Task) (*protocol.Result, *protocol.APIError) {
	if reuse != nil {
		if dispatchErr := e.beginUIDispatch(work, "focus"); dispatchErr != nil {
			return e.finishAppError(op, "dispatch", &protocol.AppView{
				Kind: protocol.AppKindOpen, Package: app,
				TaskID: appIntPtr(reuse.TaskID), DisplayID: appIntPtr(0),
				Ownership: TaskShared, Reused: appBoolPtr(true),
			}, dispatchErr)
		}
		receipt, focusErr := e.dev.FocusTask(work.Ctx, reuse.TaskID)
		e.recordUIReceipt(work, receipt)
		if receipt == device.DispatchNotDispatched {
			return e.finishOpenNotDispatched(work, op, session, app, reuse.TaskID, true)
		}
		if focusErr != nil || receipt != device.DispatchTerminated {
			e.markDispatchUnknown(work)
			return e.finishAppUncertainWork(work, op, &protocol.AppView{
				Kind: protocol.AppKindOpen, Package: app,
				TaskID: appIntPtr(reuse.TaskID), DisplayID: appIntPtr(0),
				Ownership: TaskShared, Reused: appBoolPtr(true),
				StatePreserved: nil, HasStatePreserved: true,
			}, protocol.CodeTaskFocus, "已请求复用窗口，但无法确认置前", nil)
		}
		if !e.verifyForeground(work, 0, app, reuse.TaskID) {
			return e.finishAppUncertainWork(work, op, &protocol.AppView{
				Kind: protocol.AppKindOpen, Package: app,
				TaskID: appIntPtr(reuse.TaskID), DisplayID: appIntPtr(0),
				Ownership: TaskShared, Reused: appBoolPtr(true),
				StatePreserved: nil, HasStatePreserved: true,
			}, protocol.CodeTaskFocus, "已请求复用窗口，但无法确认置前", nil)
		}
		e.clearDispatchUnknown(work)
		return e.finishAppExecuted(work, op, &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: app,
			TaskID: appIntPtr(reuse.TaskID), DisplayID: appIntPtr(0),
			Ownership: TaskShared, Reused: appBoolPtr(true),
			StatePreserved:    e.reuseStatePreserved(work, session, reuse),
			HasStatePreserved: true,
		})
	}
	if dispatchErr := e.beginUIDispatch(work, "launch"); dispatchErr != nil {
		return e.finishAppError(op, "dispatch", &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: app, DisplayID: appIntPtr(0),
			Ownership: TaskShared,
		}, dispatchErr)
	}
	launch, launchErr := e.dev.LaunchApp(work.Ctx, 0, component)
	e.recordUIReceipt(work, launch.Receipt)
	if launch.Receipt == device.DispatchNotDispatched {
		return e.finishOpenNotDispatched(work, op, session, app, 0, false)
	}
	if launchErr != nil || launch.State == device.LaunchUnknown || launch.Receipt == device.DispatchUnknown {
		e.markDispatchUnknown(work)
		return e.finishAppUncertainWork(work, op, &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: app, DisplayID: appIntPtr(0),
			Ownership: TaskShared, Reused: appBoolPtr(false),
			StatePreserved: nil, HasStatePreserved: true,
		}, protocol.CodeAppLaunchUnknown, "已派发应用启动，但无法确认结果", nil)
	}
	if launch.State == device.LaunchRejected || launch.State == device.LaunchNotStarted {
		e.clearDispatchUnknown(work)
		return e.finishAppError(op, "dispatch", &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: app, DisplayID: appIntPtr(0),
			Ownership: TaskShared,
		}, protocol.NewError(503, protocol.CodeAppLaunchFailed, "应用启动被设备拒绝"))
	}
	task, moved, ok := e.verifyLaunchedTask(work, session, app, preAllTasks)
	if !ok || moved {
		e.markDispatchUnknown(work)
		code := protocol.CodeAppLaunchUnknown
		if moved {
			code = protocol.CodeAppMoved
		}
		return e.finishAppUncertainWork(work, op, &protocol.AppView{
			Kind: protocol.AppKindOpen, Package: app, DisplayID: appIntPtr(0),
			Ownership: TaskShared, Reused: appBoolPtr(false),
			StatePreserved: nil, HasStatePreserved: true,
		}, code, "已派发应用启动，但无法确认任务归属", nil)
	}
	e.clearDispatchUnknown(work)
	return e.finishAppExecuted(work, op, &protocol.AppView{
		Kind: protocol.AppKindOpen, Package: app,
		TaskID: appIntPtr(task.TaskID), DisplayID: appIntPtr(0),
		Ownership: TaskShared, Reused: appBoolPtr(false),
		StatePreserved: appBoolPtr(false), HasStatePreserved: true,
	})
}

// reuseStatePreserved compares the same task's top activity before and after the
// focus dispatch. A missing comparison stays null rather than true.
func (e *Engine) reuseStatePreserved(work *workState, session *sessionState, before *device.Task) *bool {
	if before == nil {
		return nil
	}
	after, err := e.dev.TaskList(work.Ctx)
	if err != nil {
		return nil
	}
	for _, task := range after {
		if task.TaskID != before.TaskID {
			continue
		}
		if task.DisplayID != before.DisplayID || task.Component != before.Component {
			return appBoolPtr(false)
		}
		return appBoolPtr(true)
	}
	return nil
}

func (e *Engine) finishOpenNotDispatched(work *workState, op *operationState, session *sessionState,
	app string, taskID int, reused bool) (*protocol.Result, *protocol.APIError) {
	code, message := protocol.CodeAppLaunchFailed, "应用启动或置前未派发"
	if e.workRevoked(work) {
		code, message = protocol.CodeStopped, "应用启动已撤销，未派发"
	}
	view := &protocol.AppView{Kind: protocol.AppKindOpen, Package: app}
	if taskID > 0 {
		view.TaskID = appIntPtr(taskID)
	}
	if session != nil {
		display := session.Display.ID
		view.DisplayID = &display
		view.Ownership = TaskOwned
		if session.Mode == "primary" {
			view.Ownership = TaskShared
		}
		view.Reused = appBoolPtr(reused)
	}
	return e.finishAppError(op, "dispatch", view, protocol.NewError(protocol.Status(code), code, message))
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
		ownership := task.Ownership
		if ownership == "" {
			ownership = TaskOwned
		}
		entry := LedgerTask{
			TaskID: task.TaskID, Package: task.Package, DisplayID: task.DisplayID,
			RootTaskID: task.RootID, Ownership: ownership, Proven: task.Proven,
			Generation: task.Generation,
		}
		if task.OriginDisplay != nil {
			entry.OriginDisplay = appIntPtr(*task.OriginDisplay)
		}
		out = append(out, entry)
	}
	return out
}

func tasksFromLedger(tasks []LedgerTask) []taskEntry {
	out := make([]taskEntry, 0, len(tasks))
	for i, task := range tasks {
		if i >= 16 || task.TaskID <= 0 {
			break
		}
		ownership := task.Ownership
		switch ownership {
		case TaskOwned, TaskBorrowed, TaskShared:
		default:
			// A version 1-4 record carries no ownership and upgrades to owned.
			// No package-name inference is ever applied.
			ownership = TaskOwned
		}
		entry := taskEntry{
			TaskID: task.TaskID, RootID: task.RootTaskID, Package: task.Package,
			DisplayID: task.DisplayID, Generation: task.Generation,
			Ownership: ownership, Proven: task.Proven,
		}
		if task.OriginDisplay != nil {
			entry.OriginDisplay = appIntPtr(*task.OriginDisplay)
		}
		out = append(out, entry)
	}
	return out
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

// verifyLaunchedTask polls the live whole-device task list for exactly one new
// task of the package. The known set is the complete pre-launch snapshot, so a
// pre-existing window on any display can never be attributed to this launch.
// moved reports a unique new task on another display. Every successful round
// recomputes the candidate from that round's snapshot only: a round that sees
// no unique new task anywhere drops the candidate of an earlier round.
func (e *Engine) verifyLaunchedTask(work *workState, session *sessionState,
	app string, preAllTasks []device.Task) (*device.Task, bool, bool) {
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
		candidate, moved = nil, false
		onTarget, elsewhere := diffNewPackageTasks(all, preAllTasks, app, session.Display.ID)
		switch {
		case len(onTarget) == 1:
			task := onTarget[0]
			return &task, false, true
		case len(onTarget) > 1:
			return nil, false, false
		case len(elsewhere) == 1:
			task := elsewhere[0]
			candidate, moved = &task, true
		}
	}
	if candidate != nil {
		return candidate, moved, true
	}
	return nil, false, false
}

// rememberTask records one proven window on the virtual session. A task that a
// transfer record still protects is never re-adopted as owned.
func (e *Engine) rememberTask(session *sessionState, task device.Task, proven bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil || e.session.Epoch != session.Epoch || e.session.State == SessionEnded {
		return
	}
	for i := range e.session.Tasks {
		if e.session.Tasks[i].TaskID == task.TaskID {
			e.session.Tasks[i].DisplayID = task.DisplayID
			e.session.Tasks[i].RootID = task.RootID
			if e.session.Tasks[i].Ownership == TaskShared {
				// A handed-off task never becomes owned again.
				return
			}
			e.session.Tasks[i].Proven = e.session.Tasks[i].Proven || proven
			return
		}
	}
	if e.transferProtectsLocked(task.TaskID) {
		return
	}
	if len(e.session.Tasks) >= 16 {
		return
	}
	e.session.Tasks = append(e.session.Tasks, taskEntry{
		TaskID: task.TaskID, RootID: task.RootID, Package: task.Package,
		DisplayID: task.DisplayID, Generation: e.session.Display.Generation,
		Ownership: TaskOwned, Proven: proven, CreatedAt: time.Now(),
	})
	e.persistAsync()
}
