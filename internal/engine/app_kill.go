package engine

import (
	"context"
	"sort"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// killPlan is the verified original process set of one kill attempt.
type killPlan struct {
	instances []device.ProcessInstance
	userID    int
	bootID    string
	pkg       string
}

// pids returns the sorted external PIDs of the plan.
func (p *killPlan) pids() []int {
	out := make([]int, 0, len(p.instances))
	for _, instance := range p.instances {
		out = append(out, instance.PID)
	}
	sort.Ints(out)
	return out
}

// appKill terminates the exact original process instances of one live owned task
// on the session display. It never uses force-stop, a UID sweep or a prefix scan.
func (e *Engine) appKill(ctx context.Context, op *operationState, req *protocol.AppRequest) (*protocol.Result, *protocol.APIError) {
	now := time.Now()
	e.mu.Lock()
	_, session, staticErr := e.appStaticCheckLocked(op.Agent, req.Reference, now)
	if staticErr == nil {
		staticErr = e.appModeGuardLocked(session, req.Kind)
	}
	var epoch int64
	if staticErr == nil {
		epoch = session.Epoch
	}
	e.mu.Unlock()
	if staticErr != nil {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind), staticErr)
	}
	if req.Target == nil || !req.Target.UseTask {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind),
			protocol.Invalid("kill 需要 target.task_id"))
	}

	work, busy := e.acquireWork(op, WorkAppAction)
	if busy != nil {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind), busy)
	}
	defer e.releaseWork(work)

	task, targetErr := e.resolveAppTarget(work.Ctx, session.Display.ID, req.Target)
	if targetErr != nil {
		return e.finishAppError(op, "preflight",
			appViewWithDisplay(req.Kind, session.Display.ID), targetErr)
	}
	view := appTargetErrorView(req, task)
	view.Kind = protocol.AppKindKill
	view.SourceTaskID = appIntPtr(task.TaskID)
	view.SourceDisplayID = nil
	view.DisplayID = appIntPtr(session.Display.ID)

	plan, planErr := e.killGuard(work, session, req, task)
	if planErr != nil {
		if plan != nil {
			view.TargetPIDs = plan.pids()
			view.HasTargetPIDs = true
		}
		return e.finishAppError(op, "preflight", view, planErr)
	}
	view.TargetPIDs = plan.pids()
	view.HasTargetPIDs = true

	e.mu.Lock()
	claimErr := e.claimObservationLocked(op, op.Agent, work, e.observations[req.Reference],
		e.session, time.Now())
	e.mu.Unlock()
	if claimErr != nil {
		return e.finishAppError(op, "claim", view, claimErr)
	}

	// Final guard immediately before the one signal.
	final, finalErr := e.killGuard(work, session, req, task)
	if finalErr != nil {
		view.TargetPIDs = plan.pids()
		view.HasTargetPIDs = true
		finalErr.Outcome = OutcomeNotExecuted
		return e.finishAppError(op, "revalidate", view, finalErr)
	}
	if !sameProcessSet(plan.instances, final.instances) {
		view.TargetPIDs = plan.pids()
		view.HasTargetPIDs = true
		return e.finishAppError(op, "revalidate", view,
			statusError(protocol.CodeKillUnconfirmed, "目标进程集合已变化，未派发信号"))
	}

	view.TargetPIDs = final.pids()
	view.HasTargetPIDs = true
	if len(final.instances) == 0 {
		// The qualified live task exists but the package has no attributable
		// process: the explicit completed branch. No signal is sent.
		view.KilledPIDs = []int{}
		view.HasKilledPIDs = true
		view.RemainingPIDs = []int{}
		view.HasRemainingPIDs = true
		view.Restarted = appBoolPtr(false)
		view.HasRestarted = true
		return e.finishAppExecuted(work, op, view)
	}

	allTerminated := true
	killed := make([]int, 0, len(final.instances))
	for _, instance := range final.instances {
		if e.workRevoked(work) {
			allTerminated = false
			break
		}
		receipt, killErr := e.dev.KillProcess(work.Ctx, instance.PID, instance.StartTicks)
		if receipt != device.DispatchTerminated || killErr != nil {
			allTerminated = false
		}
	}

	remaining, readOK := e.awaitKillVerification(work, final)
	if readOK {
		for _, instance := range final.instances {
			if !instancePresent(remaining, instance) {
				killed = append(killed, instance.PID)
			}
		}
	} else {
		// Without a complete post read only already-proven terminations count.
		killed = nil
	}
	sort.Ints(killed)
	view.KilledPIDs = killed
	view.HasKilledPIDs = true

	originalGone := readOK && len(killed) == len(final.instances)
	if readOK {
		view.RemainingPIDs = processPIDs(remaining)
		view.HasRemainingPIDs = true
		view.Restarted = appBoolPtr(restarted(remaining, final.instances))
		view.HasRestarted = true
	} else {
		view.Restarted = nil
		view.HasRestarted = true
	}

	if originalGone && allTerminated {
		return e.finishAppExecuted(work, op, view)
	}
	_ = epoch
	return e.finishAppUncertainWork(work, op, view, protocol.CodeKillUnconfirmed,
		"已派发进程终止，但无法确认原实例全部结束", nil)
}

// killGuard verifies every pre-dispatch condition and reads the original process
// set. A non-nil error never authorizes a signal.
func (e *Engine) killGuard(work *workState, session *sessionState, req *protocol.AppRequest,
	task *device.Task) (*killPlan, *protocol.APIError) {
	if e.workRevoked(work) {
		return nil, protocol.NewError(409, protocol.CodeStopped, "进程终止已撤销")
	}
	if task.Package == e.cfg.ControlPackage {
		return nil, protocol.NewError(409, protocol.CodeControlUI, "不能终止控制界面进程")
	}
	all, err := e.dev.TaskList(work.Ctx)
	if err != nil {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取任务清单")
	}
	var live *device.Task
	for i := range all {
		if all[i].TaskID == task.TaskID {
			live = &all[i]
		}
	}
	if live == nil || live.DisplayID != session.Display.ID || live.Package != task.Package {
		return nil, protocol.NewError(409, protocol.CodeTaskNotTracked, "任务已不在本会话显示屏")
	}
	e.mu.Lock()
	entry := e.sessionTaskLocked(e.session, task.TaskID)
	e.mu.Unlock()
	if entry == nil || entry.Ownership != TaskOwned {
		return nil, protocol.NewError(409, protocol.CodeKillNotOwned, "只有本会话自有任务可终止进程")
	}
	otherDisplay := false
	sibling := false
	for _, candidate := range all {
		if candidate.Package != task.Package || candidate.TaskID == task.TaskID {
			continue
		}
		if candidate.DisplayID != session.Display.ID {
			otherDisplay = true
		} else if candidate.DisplayID == session.Display.ID {
			sibling = true
		}
	}
	if otherDisplay {
		return nil, protocol.NewError(409, protocol.CodeKillTaskElsewhere,
			"该应用在其他显示屏仍有任务")
	}
	if sibling {
		return nil, protocol.NewError(409, protocol.CodeKillProcessShared,
			"该应用在本屏还有其他窗口，进程终止会连带影响")
	}

	foreground, fgErr := e.dev.Foreground(work.Ctx, session.Display.ID)
	if fgErr != nil || foreground.State == device.ForegroundUnknown {
		return nil, protocol.NewError(409, protocol.CodeKillForeground,
			"无法确认本屏前台，拒绝终止进程")
	}
	if foreground.State == device.ForegroundPresent && foreground.Package == task.Package {
		return nil, protocol.NewError(409, protocol.CodeKillForeground,
			"应用正在本屏前台，拒绝终止进程")
	}

	services, serviceErr := e.dev.ForegroundService(work.Ctx, task.Package)
	media, mediaErr := e.dev.MediaPlayback(work.Ctx)
	if serviceErr != nil || mediaErr != nil || !services.Known || !media.Known {
		return nil, protocol.NewError(409, protocol.CodeKillPlayback,
			"无法确认前台服务与播放状态，拒绝终止进程")
	}
	if (services.Foreground || media.Playing) && !req.AllowStopPlayback {
		return nil, protocol.NewError(409, protocol.CodeKillPlayback,
			"应用有前台服务或正在播放；需要显式允许才能中断")
	}

	root, rootErr := e.dev.RootAvailable(work.Ctx)
	if rootErr != nil || !root {
		return nil, protocol.NewError(409, protocol.CodeRootUnavailable,
			"root 不可用，拒绝终止进程")
	}

	identity, identityErr := e.transferIdentity(work.Ctx)
	if identityErr != nil {
		return nil, identityErr
	}
	processes, processErr := e.dev.PackageProcesses(work.Ctx, task.Package)
	if processErr != nil || !processes.Known {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable,
			"无法读取应用进程清单")
	}
	if processes.BootID != "" && identity.BootID != "" && processes.BootID != identity.BootID {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable,
			"无法确认当前启动身份")
	}
	if processes.UserID <= 0 {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable,
			"无法确认应用用户身份")
	}
	instances := append([]device.ProcessInstance(nil), processes.Instances...)
	sort.Slice(instances, func(i, j int) bool { return instances[i].PID < instances[j].PID })
	return &killPlan{instances: instances, userID: processes.UserID,
		bootID: identity.BootID, pkg: task.Package}, nil
}

// awaitKillVerification polls the package process inventory after the signal. A
// failed read stays unknown; it is never reported as an empty process set.
func (e *Engine) awaitKillVerification(work *workState, plan *killPlan) ([]device.ProcessInstance, bool) {
	var last []device.ProcessInstance
	read := false
	for _, delay := range []time.Duration{0, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond} {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-work.Ctx.Done():
				timer.Stop()
				return last, read
			}
			timer.Stop()
		}
		processes, err := e.dev.PackageProcesses(work.Ctx, plan.pkg)
		if err != nil || !processes.Known {
			continue
		}
		last = processes.Instances
		read = true
		gone := true
		for _, instance := range plan.instances {
			if instancePresent(last, instance) {
				gone = false
				break
			}
		}
		if gone {
			return last, true
		}
	}
	return last, read
}

// instancePresent reports whether the exact PID+boot-start identity is present.
func instancePresent(instances []device.ProcessInstance, want device.ProcessInstance) bool {
	for _, instance := range instances {
		if instance.PID == want.PID && instance.StartTicks == want.StartTicks {
			return true
		}
	}
	return false
}

// sameProcessSet reports whether two inventories carry the same identities.
func sameProcessSet(a, b []device.ProcessInstance) bool {
	if len(a) != len(b) {
		return false
	}
	for _, instance := range a {
		if !instancePresent(b, instance) {
			return false
		}
	}
	return true
}

// processPIDs returns the sorted external PIDs.
func processPIDs(instances []device.ProcessInstance) []int {
	out := make([]int, 0, len(instances))
	for _, instance := range instances {
		out = append(out, instance.PID)
	}
	sort.Ints(out)
	return out
}

// restarted reports whether the current set contains an instance that is not one
// of the original identities. Original survivors are not a restart.
func restarted(current, original []device.ProcessInstance) bool {
	for _, instance := range current {
		if !instancePresent(original, instance) {
			return true
		}
	}
	return false
}
