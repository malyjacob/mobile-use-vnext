package engine

import (
	"context"
	"sort"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// appClose removes an exact frozen set of session-managed windows on the session
// display. It is one bounded plan, never a package-wide sweep of the device.
func (e *Engine) appClose(ctx context.Context, op *operationState, req *protocol.AppRequest) (*protocol.Result, *protocol.APIError) {
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

	work, busy := e.acquireWork(op, WorkAppAction)
	if busy != nil {
		return e.finishAppError(op, "admission", appKindOnly(req.Kind), busy)
	}
	defer e.releaseWork(work)

	targets, targetErr := e.freezeCloseTargets(work.Ctx, session, req)
	if targetErr != nil {
		return e.finishAppError(op, "preflight",
			appViewWithDisplay(req.Kind, session.Display.ID), targetErr)
	}
	if len(targets) > e.cfg.MaxCloseTasks {
		return e.finishAppError(op, "preflight",
			appViewWithDisplay(req.Kind, session.Display.ID),
			capacityError("本次关闭目标数量超过上限", e.cfg.MaxCloseTasks))
	}
	for _, target := range targets {
		if target.ownership == TaskBorrowed && !req.AllowBorrowed {
			return e.finishAppError(op, "preflight",
				appViewWithDisplay(req.Kind, session.Display.ID),
				protocol.NewError(409, protocol.CodeCloseBorrowed,
					"目标包含借入窗口；默认不销毁会话借入的任务"))
		}
	}

	e.mu.Lock()
	claimErr := e.claimObservationLocked(op, op.Agent, work, e.observations[req.Reference],
		e.session, time.Now())
	e.mu.Unlock()
	if claimErr != nil {
		return e.finishAppError(op, "claim",
			appViewWithDisplay(req.Kind, session.Display.ID), claimErr)
	}

	items := make([]protocol.AppCloseTask, 0, len(targets))
	removed := map[int]bool{}
	var firstErr *protocol.APIError
	halted := false
	for _, target := range targets {
		item := protocol.AppCloseTask{TaskID: target.task.TaskID}
		if halted {
			item.Outcome = OutcomeNotExecuted
			item.Error = protocol.NewError(409, protocol.CodeStopped, "任务关闭已停止").Public()
			items = append(items, item)
			continue
		}
		if e.workRevoked(work) {
			item.Outcome = OutcomeNotExecuted
			item.Error = protocol.NewError(409, protocol.CodeStopped, "任务关闭已撤销").Public()
			items = append(items, item)
			firstErr = statusError(protocol.CodeStopped, "任务关闭已撤销")
			halted = true
			continue
		}
		if guardErr := e.revalidateCloseTarget(work, session, target); guardErr != nil {
			item.Outcome = OutcomeNotExecuted
			item.Error = guardErr.Public()
			items = append(items, item)
			if firstErr == nil {
				firstErr = guardErr
			}
			halted = true
			continue
		}
		result, removeErr := e.dev.RemoveTask(work.Ctx, target.task.TaskID)
		switch result.Receipt {
		case device.DispatchTerminated:
			if removeErr != nil {
				item.Outcome = OutcomeUncertain
				item.Error = statusError(protocol.CodeAppTaskUnconfirmed,
					"任务关闭回执不完整，请先核验现状").Public()
				if firstErr == nil {
					firstErr = statusError(protocol.CodeAppTaskUnconfirmed,
						"任务关闭回执不完整，请先核验现状")
				}
			} else {
				item.Outcome = OutcomeExecuted
				removed[target.task.TaskID] = true
			}
		case device.DispatchNotDispatched:
			item.Outcome = OutcomeNotExecuted
			item.Error = statusError(protocol.CodeStopped, "任务关闭未派发").Public()
			if firstErr == nil {
				firstErr = statusError(protocol.CodeStopped, "任务关闭未派发")
			}
		default:
			item.Outcome = OutcomeUncertain
			item.Error = statusError(protocol.CodeAppTaskUnconfirmed,
				"任务关闭结果未确认，请先核验现状").Public()
			if firstErr == nil {
				firstErr = statusError(protocol.CodeAppTaskUnconfirmed,
					"任务关闭结果未确认，请先核验现状")
			}
		}
		items = append(items, item)
		if item.Outcome != OutcomeExecuted {
			halted = true
		}
	}

	// RecentTasks verification is a separate read and never a replacement for
	// the removal receipts.
	recents, recentsKnown := e.readCleanupRecents(work.Ctx)
	for i := range items {
		switch items[i].Outcome {
		case OutcomeExecuted, OutcomeUncertain:
			items[i].HasRecentsCleaned = true
			if recentsKnown {
				items[i].RecentsCleaned = appBoolPtr(!recents[items[i].TaskID])
			} else {
				items[i].RecentsCleaned = nil
			}
		}
	}
	allExecuted := len(items) > 0
	allNotExecuted := len(items) > 0
	complete := len(items) > 0
	for i := range items {
		if items[i].Outcome != OutcomeExecuted {
			allExecuted = false
			complete = false
		}
		if items[i].Outcome != OutcomeNotExecuted {
			allNotExecuted = false
		}
		if items[i].RecentsCleaned == nil || !*items[i].RecentsCleaned {
			complete = false
		}
	}

	e.mu.Lock()
	if e.session != nil && e.session.Epoch == epoch {
		for id := range removed {
			e.removeSessionTaskLocked(id)
		}
	}
	e.mu.Unlock()
	e.persistAsync()

	view := &protocol.AppView{
		Kind: protocol.AppKindClose, DisplayID: appIntPtr(session.Display.ID),
		Tasks: items, HasTasks: true, Complete: appBoolPtr(complete),
	}
	switch {
	case allExecuted:
		return e.finishAppExecuted(work, op, view)
	case allNotExecuted:
		if firstErr == nil {
			firstErr = statusError(protocol.CodeTaskNotFound, "没有可关闭的任务")
		}
		firstErr.Outcome = OutcomeNotExecuted
		return e.finishAppError(op, "dispatch", view, firstErr)
	default:
		if firstErr == nil {
			firstErr = statusError(protocol.CodeAppTaskUnconfirmed,
				"部分任务的关闭结果未确认，请先核验现状")
		}
		return e.finishAppUncertainWork(work, op, view, firstErr.Code, firstErr.Message, firstErr.Details)
	}
}

// statusError builds one controlled business conflict error.
func statusError(code, message string) *protocol.APIError {
	return protocol.NewError(protocol.Status(code), code, message)
}

// closeTarget is one frozen close target with its already-proven ownership.
type closeTarget struct {
	task      device.Task
	ownership string
}

// freezeCloseTargets resolves the admitted selector into the exact bounded plan
// before the observation is consumed. Package matching freezes every matching
// managed window; a task-id selector must name one managed window.
func (e *Engine) freezeCloseTargets(ctx context.Context, session *sessionState,
	req *protocol.AppRequest) ([]closeTarget, *protocol.APIError) {
	all, err := e.dev.TaskList(ctx)
	if err != nil {
		return nil, protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取任务清单")
	}
	e.mu.Lock()
	entries := map[int]taskEntry{}
	for _, entry := range e.session.Tasks {
		entries[entry.TaskID] = entry
	}
	epochSession := e.session != nil && e.session.Epoch == session.Epoch
	e.mu.Unlock()
	if !epochSession {
		return nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	var targets []closeTarget
	if req.Target.UseTask {
		var found *device.Task
		for i := range all {
			if all[i].TaskID == req.Target.TaskID {
				found = &all[i]
			}
		}
		if found == nil || found.DisplayID != session.Display.ID {
			return nil, protocol.NewError(409, protocol.CodeTaskNotFound, "找不到该任务")
		}
		entry, ok := entries[found.TaskID]
		if !ok || (entry.Ownership != TaskOwned && entry.Ownership != TaskBorrowed) {
			return nil, protocol.NewError(409, protocol.CodeTaskNotTracked,
				"该任务不属于本会话")
		}
		targets = append(targets, closeTarget{task: *found, ownership: entry.Ownership})
	} else {
		for _, task := range all {
			if task.DisplayID != session.Display.ID || task.Package != req.Target.App {
				continue
			}
			entry, ok := entries[task.TaskID]
			if !ok || (entry.Ownership != TaskOwned && entry.Ownership != TaskBorrowed) {
				continue
			}
			targets = append(targets, closeTarget{task: task, ownership: entry.Ownership})
		}
		sort.Slice(targets, func(i, j int) bool {
			return targets[i].task.TaskID < targets[j].task.TaskID
		})
	}
	if len(targets) == 0 {
		return nil, protocol.NewError(409, protocol.CodeTaskNotFound,
			"没有符合条件的会话窗口")
	}
	return targets, nil
}

// revalidateCloseTarget proves the exact task is still live on the session
// display, still managed by this session and not protected by a transfer.
func (e *Engine) revalidateCloseTarget(work *workState, session *sessionState, target closeTarget) *protocol.APIError {
	if e.workRevoked(work) {
		return protocol.NewError(409, protocol.CodeStopped, "任务关闭已撤销")
	}
	all, err := e.dev.TaskList(work.Ctx)
	if err != nil {
		return protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取任务清单")
	}
	var found *device.Task
	for i := range all {
		if all[i].TaskID == target.task.TaskID {
			found = &all[i]
		}
	}
	if found == nil || found.DisplayID != session.Display.ID || found.Package != target.task.Package {
		return protocol.NewError(409, protocol.CodeTaskNotTracked, "任务已不在本会话显示屏")
	}
	e.mu.Lock()
	entry := e.sessionTaskLocked(e.session, target.task.TaskID)
	protected := e.transferProtectsLocked(target.task.TaskID)
	e.mu.Unlock()
	if entry == nil || (entry.Ownership != TaskOwned && entry.Ownership != TaskBorrowed) {
		return protocol.NewError(409, protocol.CodeTaskNotTracked, "任务已不再受本会话管理")
	}
	if protected {
		return protocol.NewError(409, protocol.CodeTaskLifecycleBusy,
			"任务仍有未确认的迁移责任")
	}
	return nil
}
