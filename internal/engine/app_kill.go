package engine

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type killPlan struct {
	Package         string
	AndroidUserID   int
	Tasks           []device.Task
	Identity        device.PackageIdentity
	Execution       device.BootIdentity
	Processes       []device.ProcessInstance
	UsedEligibility bool
}

func (e *Engine) appKill(_ context.Context, op *operationState, req *protocol.AppRequest) (*protocol.Result, *protocol.APIError) {
	e.mu.Lock()
	obs, session, staticErr := e.appStaticCheckLocked(op.Agent, req.Reference, time.Now())
	if staticErr == nil {
		staticErr = e.appModeGuardLocked(session, req.Kind)
	}
	var epoch int64
	var display device.Display
	if staticErr == nil {
		epoch, display = session.Epoch, session.Display
		if obs.DisplayID != display.ID || obs.DisplayGeneration != display.Generation {
			// The observation and every close authorization bound to its display
			// generation are stale together.
			session.CloseEligibilities = nil
			staticErr = protocol.NewError(409, protocol.CodeObservationRetired, "观察的显示屏已变化")
		}
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

	view := appViewWithDisplay(req.Kind, display.ID)
	view.Package = req.Target.App
	plan, targetErr := e.freezeKillTarget(work, epoch, display, req.Target, view)
	if targetErr != nil {
		return e.finishAppError(op, "preflight", view, targetErr)
	}
	plan, guardErr := e.killGuard(work, epoch, display, req, plan)
	if guardErr != nil {
		return e.finishAppError(op, "preflight", view, guardErr)
	}
	view.TargetPIDs = processPIDs(plan.Processes)
	view.HasTargetPIDs = true

	e.mu.Lock()
	now := time.Now()
	claimErr := e.killBindingErrorLocked(work, epoch, display, now)
	if claimErr == nil {
		claimErr = e.validateKillEligibilityLocked(plan, plan.Identity, plan.Execution, now)
	}
	if claimErr == nil {
		claimErr = e.claimObservationLocked(op, op.Agent, work, e.observations[req.Reference], e.session, now)
	}
	e.mu.Unlock()
	if claimErr != nil {
		return e.finishAppError(op, "claim", view, claimErr)
	}

	final, guardErr := e.killGuard(work, epoch, display, req, plan)
	if guardErr != nil {
		return e.finishAppError(op, "revalidate", view, guardErr)
	}
	view.TargetPIDs = processPIDs(final.Processes)

	// STOP cancels the adapter context. Mark the dispatch window in the same
	// lock that checks authority, so a detached P can never report that a
	// command which may be running was definitely unissued.
	e.mu.Lock()
	now = time.Now()
	dispatchErr := e.killBindingErrorLocked(work, epoch, display, now)
	if dispatchErr == nil {
		dispatchErr = e.validateKillEligibilityLocked(final, final.Identity, final.Execution, now)
	}
	if dispatchErr == nil {
		work.DispatchUnknown = true
	}
	e.mu.Unlock()
	if dispatchErr != nil {
		return e.finishAppError(op, "dispatch", view, dispatchErr)
	}

	result, _ := e.dev.ForceStopPackage(work.Ctx, final.Package, final.AndroidUserID)
	confirmed := e.recordKillReceipt(work, op, epoch, display, final, result)
	if result.Receipt == device.DispatchNotDispatched {
		return e.finishAppError(op, "dispatch", view,
			protocol.NewError(409, protocol.CodeStopped, "包级强停未派发"))
	}

	sample := e.sampleKillResult(work, final)
	sample.apply(view, final.Processes)
	e.forgetAbsentKillTasks(work, epoch, display, final.Tasks, sample.taskIDs, sample.tasksKnown)
	if confirmed {
		return e.finishAppExecuted(work, op, view)
	}
	return e.finishAppUncertainWork(work, op, view, protocol.CodeKillUnconfirmed,
		"已派发包级强停，但无法确认命令成功", nil)
}

func (e *Engine) readKillTasks(ctx context.Context) ([]device.Task, *protocol.APIError) {
	snapshot, err := e.dev.TaskSnapshot(ctx)
	if err != nil {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取任务清单")
	}
	if !snapshot.Complete {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "任务枚举不完整")
	}
	return snapshot.Tasks, nil
}

func killPackageTasks(all []device.Task, pkg string) []device.Task {
	var tasks []device.Task
	for _, task := range all {
		if task.Package == pkg {
			tasks = append(tasks, task)
		}
	}
	slices.SortFunc(tasks, func(a, b device.Task) int { return cmp.Compare(a.TaskID, b.TaskID) })
	return tasks
}

func (e *Engine) freezeKillTarget(work *workState, epoch int64, display device.Display,
	target *protocol.AppTarget, view *protocol.AppView) (killPlan, *protocol.APIError) {
	var plan killPlan
	if e.workRevoked(work) {
		return plan, stoppedKill()
	}
	all, apiErr := e.readKillTasks(work.Ctx)
	if apiErr != nil {
		return plan, apiErr
	}
	if target.UseTask {
		for _, task := range all {
			if task.TaskID == target.TaskID {
				if view.SourceTaskID != nil {
					view.SourceTaskID, view.Package = nil, ""
					return plan, protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法确认唯一源任务")
				}
				view.SourceTaskID = appIntPtr(task.TaskID)
				view.Package = task.Package
			}
		}
		if view.SourceTaskID == nil {
			return plan, protocol.NewError(409, protocol.CodeTaskNotFound, "找不到该任务")
		}
	}
	if !protocol.ValidPackageName(view.Package) {
		return plan, protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法确认任务所属应用")
	}
	if view.Package == e.cfg.ControlPackage {
		return plan, protocol.NewError(409, protocol.CodeControlUI, "不能终止控制界面进程")
	}

	plan.Package = view.Package
	plan.Tasks = killPackageTasks(all, plan.Package)
	elsewhere := killHasTaskElsewhere(plan.Tasks, display.ID)
	e.mu.Lock()
	now := time.Now()
	apiErr = e.killBindingErrorLocked(work, epoch, display, now)
	if apiErr == nil {
		apiErr = e.killOwnershipLocked(plan.Package, plan.Tasks, elsewhere, target.UseTask)
	}
	if apiErr == nil && len(plan.Tasks) == 0 {
		view.TargetTaskIDs = []int{}
		view.HasTargetTaskIDs = true
		eligibility := closeEligibilityLocked(e.session, plan.Package, now)
		if eligibility == nil {
			apiErr = missingKillEligibility()
		} else {
			plan.UsedEligibility = true
			plan.AndroidUserID = eligibility.AndroidUserID
		}
	}
	e.mu.Unlock()
	if apiErr != nil {
		return plan, apiErr
	}

	view.TargetTaskIDs = make([]int, 0, len(plan.Tasks))
	view.HasTargetTaskIDs = true
	if len(plan.Tasks) > 0 {
		plan.AndroidUserID = plan.Tasks[0].UserID
	}
	for _, task := range plan.Tasks {
		view.TargetTaskIDs = append(view.TargetTaskIDs, task.TaskID)
	}
	for _, task := range plan.Tasks {
		if !task.HasUserID || task.UserID < 0 || task.UserID != plan.AndroidUserID {
			return plan, protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法确认任务所属 Android 用户")
		}
	}
	view.AndroidUserID = appIntPtr(plan.AndroidUserID)
	return plan, nil
}

func killHasTaskElsewhere(tasks []device.Task, displayID int) bool {
	for _, task := range tasks {
		if task.DisplayID != displayID {
			return true
		}
	}
	return false
}

func (e *Engine) killOwnershipLocked(pkg string, tasks []device.Task, elsewhere, useTask bool) *protocol.APIError {
	// Compare against the session registration as well as the task ID, which
	// Android may reuse. A matching registration on an external display still
	// fails the location guard, but only unowned use invalidates eligibility.
	notOwned := false
	for i, task := range tasks {
		entry := e.sessionTaskLocked(e.session, task.TaskID)
		if task.TaskID <= 0 || i > 0 && task.TaskID == tasks[i-1].TaskID ||
			entry == nil || entry.Ownership != TaskOwned || !entry.Proven || entry.Package != pkg ||
			entry.DisplayID != e.session.Display.ID || entry.Generation != e.session.Display.Generation {
			notOwned = true
			revokeCloseEligibilityLocked(e.session, pkg)
			break
		}
	}
	if elsewhere {
		return protocol.NewError(409, protocol.CodeKillTaskElsewhere, "该应用在其他显示屏仍有任务")
	}
	if notOwned {
		return protocol.NewError(409, protocol.CodeKillNotOwned, "目标包含非本会话自有任务")
	}
	for _, transfer := range e.transfers {
		if transfer == nil || transfer.record.State == TransferReleased {
			continue
		}
		// A not-yet-confirmed move may temporarily be absent from the live scan.
		if !useTask && transfer.record.Package == pkg || slices.ContainsFunc(tasks, func(task device.Task) bool {
			return task.TaskID == transfer.record.LeafTaskID
		}) {
			return protocol.NewError(409, protocol.CodeTaskLifecycleBusy, "任务仍有未确认的迁移责任")
		}
	}
	return nil
}

func missingKillEligibility() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeKillNotOwned, "没有本会话自有任务或已确认关闭资格")
}

func stoppedKill() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeStopped, "包级强停已撤销")
}

func (e *Engine) killBindingErrorLocked(work *workState, epoch int64, display device.Display, now time.Time) *protocol.APIError {
	if e.session == nil {
		return stoppedKill()
	}
	if !e.workAuthorizedLocked(work, now) || e.session.Epoch != epoch {
		return stoppedKill()
	}
	if e.session.Display.ID != display.ID || e.session.Display.Generation != display.Generation {
		// A changed display generation invalidates every close authorization
		// bound to it; the record must not survive for a later window.
		e.session.CloseEligibilities = nil
		return stoppedKill()
	}
	return nil
}

// validateKillEligibilityLocked revokes known stale records. An unreadable
// installation identity cannot establish a mismatch or authorize dispatch.
func (e *Engine) validateKillEligibilityLocked(plan killPlan, identity device.PackageIdentity,
	execution device.BootIdentity, now time.Time) *protocol.APIError {
	pruneCloseEligibilitiesLocked(e.session, now)
	for _, record := range e.session.CloseEligibilities {
		if record.Package != plan.Package {
			continue
		}
		changed := record.ExecutionIdentity != execution ||
			record.AndroidUserID == plan.AndroidUserID && identity.Known && !record.matches(identity, execution)
		if changed {
			revokeCloseEligibilityLocked(e.session, plan.Package)
			if plan.UsedEligibility {
				return protocol.NewError(409, protocol.CodeKillNotOwned, "应用安装身份已变化")
			}
			break
		}
	}
	if plan.UsedEligibility {
		record := closeEligibilityLocked(e.session, plan.Package, now)
		if record == nil || record.AndroidUserID != plan.AndroidUserID {
			return missingKillEligibility()
		}
	}
	return nil
}

func validKillPackageIdentity(identity device.PackageIdentity, pkg string, user int) bool {
	return identity.Known && identity.Package == pkg && user >= 0 && identity.AndroidUserID == user &&
		identity.AppID >= 10000 && identity.AppID < 100000 && identity.AppUID/100000 == user &&
		identity.AppUID%100000 == identity.AppID && identity.FirstInstallTime != "" && identity.SignatureHash != ""
}

// killGuard re-reads all pre-dispatch evidence. Only the first pass may fill
// the frozen device, installation and process identities.
func (e *Engine) killGuard(work *workState, epoch int64, display device.Display,
	req *protocol.AppRequest, plan killPlan) (killPlan, *protocol.APIError) {
	if e.workRevoked(work) {
		return plan, stoppedKill()
	}
	all, apiErr := e.readKillTasks(work.Ctx)
	if apiErr != nil {
		return plan, apiErr
	}
	tasks := killPackageTasks(all, plan.Package)
	elsewhere := killHasTaskElsewhere(tasks, display.ID)
	e.mu.Lock()
	apiErr = e.killBindingErrorLocked(work, epoch, display, time.Now())
	if apiErr == nil {
		apiErr = e.killOwnershipLocked(plan.Package, tasks, elsewhere, req.Target.UseTask)
	}
	e.mu.Unlock()
	if apiErr != nil {
		return plan, apiErr
	}
	if !slices.EqualFunc(plan.Tasks, tasks, sameKillTask) {
		return plan, protocol.NewError(409, protocol.CodeTaskNotTracked, "应用任务集合或所属用户已变化")
	}

	services, serviceErr := e.dev.ForegroundService(work.Ctx, plan.Package)
	media, mediaErr := e.dev.MediaPlayback(work.Ctx)
	if serviceErr != nil || mediaErr != nil || !services.Known || !media.Known {
		return plan, protocol.NewError(409, protocol.CodeKillPlayback, "无法确认前台服务与播放状态，拒绝强停应用")
	}
	if (services.Foreground || media.Playing) && !req.AllowStopPlayback {
		return plan, protocol.NewError(409, protocol.CodeKillPlayback, "应用有前台服务或正在播放；需要显式允许才能中断").
			WithDetail("retry_parameter", "allow_stop_playback")
	}
	root, rootErr := e.dev.RootAvailable(work.Ctx)
	if rootErr != nil || !root {
		return plan, protocol.NewError(409, protocol.CodeRootUnavailable, "root 不可用，拒绝强停应用")
	}

	execution, apiErr := e.transferIdentity(work.Ctx)
	if apiErr != nil {
		return plan, apiErr
	}
	identity, identityErr := e.dev.PackageIdentity(work.Ctx, plan.Package, plan.AndroidUserID)
	identityKnown := identityErr == nil && validKillPackageIdentity(identity, plan.Package, plan.AndroidUserID)
	if !identityKnown {
		identity = device.PackageIdentity{}
	}
	e.mu.Lock()
	now := time.Now()
	apiErr = e.killBindingErrorLocked(work, epoch, display, now)
	if apiErr == nil {
		apiErr = e.validateKillEligibilityLocked(plan, identity, execution, now)
	}
	e.mu.Unlock()
	if apiErr != nil {
		return plan, apiErr
	}
	if !identityKnown || plan.Identity.Known && (plan.Identity != identity || plan.Execution != execution) {
		return plan, protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法确认应用安装身份")
	}

	processes, ok := e.readKillProcesses(work.Ctx, identity, execution)
	if !ok {
		return plan, protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取应用进程清单")
	}
	plan.Identity, plan.Execution, plan.Processes = identity, execution, processes
	return plan, nil
}

func sameKillTask(a, b device.Task) bool {
	return a.TaskID == b.TaskID && a.RootID == b.RootID && a.DisplayID == b.DisplayID &&
		a.Package == b.Package && a.HasUserID && b.HasUserID && a.UserID == b.UserID
}

func (e *Engine) readKillProcesses(ctx context.Context, identity device.PackageIdentity,
	execution device.BootIdentity) ([]device.ProcessInstance, bool) {
	processes, err := e.dev.PackageProcesses(ctx, identity.Package, identity.AndroidUserID)
	if err != nil || !processes.Known || processes.ReadFailed || processes.AndroidUserID != identity.AndroidUserID ||
		processes.AppUID != identity.AppID || processes.BootID != execution.BootID {
		return nil, false
	}
	instances := slices.Clone(processes.Instances)
	slices.SortFunc(instances, func(a, b device.ProcessInstance) int { return cmp.Compare(a.PID, b.PID) })
	for i, instance := range instances {
		if instance.PID <= 0 || instance.StartTicks == 0 || i > 0 && instance.PID == instances[i-1].PID {
			return nil, false
		}
	}
	return instances, true
}

func (e *Engine) recordKillReceipt(work *workState, op *operationState, epoch int64, display device.Display,
	plan killPlan, result device.ForceStopResult) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	sameSession := e.session != nil && e.session.State == SessionActive && e.session.Epoch == epoch &&
		e.session.Display.ID == display.ID && e.session.Display.Generation == display.Generation
	if result.Receipt == device.DispatchNotDispatched {
		work.DispatchUnknown = false
		return false
	}
	if sameSession && e.work == work && !work.Revoked {
		consumeCloseEligibilityLocked(e.session, plan.Package, plan.AndroidUserID)
		if result.Receipt == device.DispatchTerminated && result.HasExitCode && result.ExitCode == 0 &&
			op.State == OperationRunning {
			work.DispatchUnknown = false
			op.KnownOutcome = OutcomeExecuted
			return true
		}
	}
	return false
}

type killSample struct {
	taskIDs        []int
	tasksKnown     bool
	processes      []device.ProcessInstance
	processesKnown bool
}

func (e *Engine) sampleKillResult(work *workState, plan killPlan) killSample {
	var sample killSample
	for _, delay := range [...]time.Duration{0, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond} {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-work.Ctx.Done():
				timer.Stop()
				return sample
			}
		}
		if work.Ctx.Err() != nil {
			break
		}
		all, taskErr := e.readKillTasks(work.Ctx)
		sample = killSample{}
		if taskErr == nil && work.Ctx.Err() == nil {
			sample.taskIDs, sample.tasksKnown = remainingKillTasks(all, plan)
		}
		if work.Ctx.Err() != nil {
			break
		}
		processes, known := e.readKillProcesses(work.Ctx, plan.Identity, plan.Execution)
		if work.Ctx.Err() == nil {
			sample.processes, sample.processesKnown = processes, known
		}
		if sample.tasksKnown && len(sample.taskIDs) == 0 && sample.processesKnown && len(sample.processes) == 0 {
			break
		}
	}
	return sample
}

func remainingKillTasks(all []device.Task, plan killPlan) ([]int, bool) {
	ids := make([]int, 0)
	for _, task := range killPackageTasks(all, plan.Package) {
		if !task.HasUserID {
			return nil, false
		}
		if task.UserID != plan.AndroidUserID {
			continue
		}
		if task.TaskID <= 0 || len(ids) > 0 && ids[len(ids)-1] == task.TaskID {
			return nil, false
		}
		ids = append(ids, task.TaskID)
	}
	return ids, true
}

func (sample killSample) apply(view *protocol.AppView, original []device.ProcessInstance) {
	if sample.tasksKnown {
		view.RemainingTaskIDs = sample.taskIDs
		view.HasRemainingTaskIDs = true
	}
	view.HasRestarted = true
	if sample.processesKnown {
		current := make(map[int]uint64, len(sample.processes))
		for _, instance := range sample.processes {
			current[instance.PID] = instance.StartTicks
		}
		view.KilledPIDs = make([]int, 0, len(original))
		survivors := 0
		for _, instance := range original {
			if current[instance.PID] == instance.StartTicks {
				survivors++
			} else {
				view.KilledPIDs = append(view.KilledPIDs, instance.PID)
			}
		}
		view.HasKilledPIDs = true
		view.RemainingPIDs = processPIDs(sample.processes)
		view.HasRemainingPIDs = true
		view.Restarted = appBoolPtr(survivors < len(sample.processes))
	}
	switch {
	case sample.tasksKnown && len(sample.taskIDs) > 0 || sample.processesKnown && len(sample.processes) > 0:
		view.PackageState = "still_present"
	case sample.tasksKnown && sample.processesKnown:
		view.PackageState = "stopped_at_check"
	default:
		view.PackageState = "unknown"
	}
}

func processPIDs(instances []device.ProcessInstance) []int {
	pids := make([]int, 0, len(instances))
	for _, instance := range instances {
		pids = append(pids, instance.PID)
	}
	return pids
}

func (e *Engine) forgetAbsentKillTasks(work *workState, epoch int64, display device.Display,
	tasks []device.Task, remaining []int, known bool) {
	if !known || len(tasks) == 0 {
		return
	}
	absent := make([]int, 0, len(tasks))
	for _, task := range tasks {
		if _, found := slices.BinarySearch(remaining, task.TaskID); !found {
			absent = append(absent, task.TaskID)
		}
	}
	if len(absent) == 0 {
		return
	}
	e.mu.Lock()
	commit := e.killBindingErrorLocked(work, epoch, display, time.Now()) == nil
	if commit {
		for _, id := range absent {
			e.removeSessionTaskLocked(id)
		}
	}
	e.mu.Unlock()
	if commit {
		e.persistAsync()
	}
}
