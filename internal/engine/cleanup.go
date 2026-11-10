package engine

import (
	"context"
	"sort"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// cleanupTarget freezes the original ownership used by the independent
// closure chain. It never carries user content.
type cleanupTarget struct {
	epoch        int64
	origin       string
	agent        string
	mode         string
	appInstance  string
	display      device.Display
	createResult *bool
	createError  string
	tasks        []taskEntry
}

func targetOf(session *sessionState) cleanupTarget {
	tasks := append([]taskEntry(nil), session.Tasks...)
	if len(tasks) > 16 {
		tasks = tasks[:16]
	}
	target := cleanupTarget{
		epoch: session.Epoch, origin: session.Origin, agent: session.Agent,
		mode: session.Mode, appInstance: session.AppInstanceID, display: session.Display,
		createResult: session.CreateResult, createError: session.CreateError,
		tasks: tasks,
	}
	return target
}

// beginCleanupLocked creates the single pending closure record. A second
// caller only observes the existing chain.
func (e *Engine) beginCleanupLocked(session *sessionState, reason string) *cleanupState {
	if session == nil || session.CleanupStarted {
		return nil
	}
	if e.cleanup != nil && e.cleanup.State == protocol.CleanupPending {
		return nil
	}
	execUnknown := e.uiPendingLocked() || (e.shell != nil && !e.shell.settled()) ||
		(e.work != nil && e.work.SessionEpoch == session.Epoch && e.work.DispatchUnknown)
	cleanup := &cleanupState{
		State: protocol.CleanupPending, Execution: protocol.CleanupConfirmed,
		Tasks: protocol.CleanupPending, Recents: protocol.CleanupPending,
		Display: protocol.CleanupPending, Reason: reason, StartedAt: time.Now(),
		evidenceRevision: e.executionEvidenceRevision,
	}
	if execUnknown {
		cleanup.Execution = protocol.CleanupUnconfirmed
		cleanup.ExecReason = e.cleanupExecReasonLocked()
		cleanup.Error = &protocol.Error{Code: "execution_unconfirmed",
			Message: "存在未确认结束的已派发输入"}
	}
	if session.Mode == "primary" {
		cleanup.Tasks = protocol.CleanupNotApplicable
		cleanup.Recents = protocol.CleanupNotApplicable
		cleanup.Display = protocol.CleanupNotApplicable
	}
	session.CleanupStarted = true
	cleanup.done = make(chan struct{})
	e.cleanup = cleanup
	e.broadcastAppLocked()
	return cleanup
}

// cleanupExecReasonLocked attributes the barrier; termination is accepted
// only for its own UI token or managed shell identity.
func (e *Engine) cleanupExecReasonLocked() string {
	if e.uiPendingLocked() {
		return "ui_input"
	}
	if e.work != nil && e.work.Kind == WorkShell {
		return "shell_chain"
	}
	if e.shell != nil && !e.shell.settled() {
		return "shell_chain"
	}
	return "ui_input"
}

// launchCleanup starts the independent closure chain for one original session.
func (e *Engine) launchCleanup(session *sessionState, reason string) {
	if session == nil {
		return
	}
	e.mu.Lock()
	cleanup := e.beginCleanupLocked(session, reason)
	target := targetOf(session)
	e.mu.Unlock()
	if cleanup == nil {
		return
	}
	e.runCleanupChain(target, cleanup)
}

// launchPreparedCleanup starts a chain whose pending record was created by the
// caller in the same state transition that revoked the session.
func (e *Engine) launchPreparedCleanup(session *sessionState, reason string,
	cleanup *cleanupState) {
	if session == nil || cleanup == nil {
		return
	}
	e.mu.Lock()
	target := targetOf(session)
	e.mu.Unlock()
	e.runCleanupChain(target, cleanup)
}

func (e *Engine) runCleanupChain(target cleanupTarget, cleanup *cleanupState) {
	e.persistAsync()
	if e.log != nil {
		e.log.Event(eventlog.Info, "cleanup.start", eventlog.Str("reason", cleanup.Reason),
			eventlog.Int("display_id", target.display.ID), eventlog.Str("mode", target.mode))
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.mu.Lock()
		result := *cleanup
		e.mu.Unlock()
		e.performCleanup(target, &result)
		e.finishCleanup(target, cleanup, result)
	}()
}

// performCleanup performs the bounded physical closure. It never runs while
// holding the state lock and never revives input authority.
func (e *Engine) performCleanup(target cleanupTarget, cleanup *cleanupState) {
	if target.mode != "virtual" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.CleanupTimeout+8*time.Second)
	defer cancel()
	displayID := target.display.ID
	generation := target.display.Generation
	if displayID == 0 && target.createResult == nil {
		// The create may have succeeded without a reported descriptor. Give the
		// App one bounded chance to report it before deciding evidence is missing.
		displayID, generation = e.waitForAppDisplay(ctx, target, 4*time.Second)
	}
	if displayID == 0 {
		switch {
		case target.createResult != nil && !*target.createResult:
			cleanup.Display = protocol.CleanupConfirmed
		case e.appReportsNoDisplay(target):
			// The App's own descriptor is absent and the helper create window has
			// passed. This is the App's physical evidence that no owned screen
			// remains; it is still an App fact, not an ADB screenshot.
			cleanup.Display = protocol.CleanupConfirmed
		default:
			cleanup.Display = protocol.CleanupUnconfirmed
			cleanup.Error = &protocol.Error{Code: "display_create_unconfirmed",
				Message: "无法确认原虚拟屏是否已建立"}
		}
		cleanup.Tasks = protocol.CleanupNotApplicable
		cleanup.Recents = protocol.CleanupNotApplicable
		return
	}

	e.mu.Lock()
	appInstance := e.app.InstanceID
	e.mu.Unlock()
	if appInstance != "" && target.appInstance == appInstance {
		e.enqueueCloseCommand(displayID, generation, appInstance)
	}

	// A virtual display is exclusively owned: every live task on it belongs to
	// this session. Remove owned windows before destroying the display, then
	// verify both the live task list and the RecentTasks index.
	hadTasks := len(target.tasks) > 0
	if live, known := e.dev.TasksOnDisplay(ctx, displayID); known && len(live) > 0 {
		hadTasks = true
	}
	if hadTasks {
		tasksOK, recentsOK, checked := e.removeOwnedTasks(ctx, displayID, target.tasks)
		cleanup.checkedTaskIDs = checked
		if tasksOK {
			cleanup.Tasks = protocol.CleanupConfirmed
		} else {
			cleanup.Tasks = protocol.CleanupUnconfirmed
			cleanup.Error = &protocol.Error{Code: protocol.CodeAppTaskUnconfirmed,
				Message: "原会话任务未能全部移除"}
		}
		if recentsOK {
			cleanup.Recents = protocol.CleanupConfirmed
		} else {
			cleanup.Recents = protocol.CleanupUnconfirmed
			if cleanup.Error == nil {
				cleanup.Error = &protocol.Error{Code: "recents_cleanup_unconfirmed",
					Message: "最近任务清理尚未确认"}
			}
		}
	} else {
		cleanup.Tasks = protocol.CleanupConfirmed
		cleanup.Recents = protocol.CleanupNotApplicable
	}

	deadline := time.Now().Add(e.cfg.CleanupTimeout)
	for {
		absent, known := e.displayConfirmedAbsent(ctx, displayID)
		if known && absent {
			cleanup.Display = protocol.CleanupConfirmed
			break
		}
		if time.Now().After(deadline) {
			if absent, known := e.displayConfirmedAbsent(ctx, displayID); known && absent {
				cleanup.Display = protocol.CleanupConfirmed
				cleanup.Error = nil
			} else {
				cleanup.Display = protocol.CleanupUnconfirmed
				cleanup.Error = &protocol.Error{Code: "display_release_unconfirmed",
					Message: "原虚拟屏释放尚未确认"}
			}
			break
		}
		e.waitAppChange(ctx, 200*time.Millisecond)
	}
}

func (e *Engine) appReportsNoDisplay(target cleanupTarget) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.app.InstanceID == "" || e.app.InstanceID != target.appInstance {
		return false
	}
	return e.app.Display == nil && e.appFreshLocked(time.Now())
}

func (e *Engine) waitForAppDisplay(ctx context.Context, target cleanupTarget,
	timeout time.Duration) (int, string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		if e.app.InstanceID == target.appInstance && e.app.Display != nil {
			display := *e.app.Display
			e.mu.Unlock()
			return display.ID, display.Generation
		}
		e.mu.Unlock()
		e.waitAppChange(ctx, 150*time.Millisecond)
	}
	return 0, ""
}

func (e *Engine) tasksOnDisplay(ctx context.Context, displayID int) []int {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tasks, err := e.dev.TaskList(queryCtx)
	if err != nil {
		return []int{-1}
	}
	var ids []int
	for _, task := range tasks {
		if task.DisplayID == displayID {
			ids = append(ids, task.TaskID)
		}
	}
	return ids
}

// displayAbsent probes the original display. known=false means the device could
// not answer; that is never treated as absence.
func (e *Engine) displayAbsent(ctx context.Context, displayID int) (bool, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := e.dev.ProbeDisplay(probeCtx, displayID)
	if err != nil {
		return false, false
	}
	return !state.Exists, true
}

func (e *Engine) waitAppChange(ctx context.Context, timeout time.Duration) {
	e.mu.Lock()
	change := e.appNotify
	e.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-change:
	case <-timer.C:
	case <-ctx.Done():
	}
}

func (e *Engine) finishCleanup(target cleanupTarget, cleanup *cleanupState, result cleanupState) {
	e.mu.Lock()
	if e.cleanup != cleanup {
		e.mu.Unlock()
		return
	}
	if cleanup.Execution == protocol.CleanupUnconfirmed {
		result.Execution = protocol.CleanupUnconfirmed
	} else if cleanup.Execution == protocol.CleanupConfirmed {
		// Release-of-barrier evidence may have promoted execution while the
		// physical chain was still running. The live record wins over the copy
		// taken when this chain started.
		result.Execution = protocol.CleanupConfirmed
	}
	if result.Execution == protocol.CleanupConfirmed && result.Error != nil &&
		result.Error.Code == "execution_unconfirmed" {
		// The promotion is the release evidence for this cause; the stale
		// begin-time error must not survive in the public closure record.
		result.Error = nil
	}
	result.needsExecutionRecheck = result.needsExecutionRecheck || result.evidenceRevision != e.executionEvidenceRevision
	complete := !result.needsExecutionRecheck && !e.uiPendingLocked() && result.Execution != protocol.CleanupUnconfirmed &&
		result.Display != protocol.CleanupUnconfirmed &&
		result.Tasks != protocol.CleanupUnconfirmed &&
		result.Recents != protocol.CleanupUnconfirmed &&
		result.Display != protocol.CleanupPending &&
		result.Tasks != protocol.CleanupPending &&
		result.Recents != protocol.CleanupPending
	if complete {
		result.Error = nil
		result.State = protocol.CleanupComplete
	} else {
		result.State = protocol.CleanupUnconfirmed
	}
	*cleanup = result
	if cleanup.done != nil {
		select {
		case <-cleanup.done:
		default:
			close(cleanup.done)
		}
	}
	if complete {
		if e.session != nil && e.session.Epoch == target.epoch {
			e.lastSession = e.session
			e.session = nil
		}
		if e.current != nil && e.current.SessionEpoch == target.epoch {
			e.current = nil
		}
		if e.baseline != nil && e.baseline.Origin == target.origin {
			e.baseline = nil
		}
		e.commands = filterCommands(e.commands, target)
	}
	e.broadcastAppLocked()
	e.mu.Unlock()
	if e.log != nil {
		fields := []eventlog.Field{
			eventlog.Str("state", cleanup.State),
			eventlog.Str("execution", cleanup.Execution),
			eventlog.Str("tasks", cleanup.Tasks),
			eventlog.Str("recents", cleanup.Recents),
			eventlog.Str("display", cleanup.Display),
			eventlog.Str("reason", cleanup.Reason),
		}
		if complete {
			e.log.Event(eventlog.Info, "cleanup.complete", fields...)
		} else {
			e.log.Event(eventlog.Warn, "cleanup.unconfirmed", fields...)
		}
	}
	e.persistAsync()
	e.launchExecutionCleanupRecheck()
}

// tasksOnDisplayByTaskList reads the complete task list and filters one
// display. Recovery uses this path so an unreadable list stays unknown.
func (e *Engine) tasksOnDisplayByTaskList(ctx context.Context, displayID int) ([]device.Task, error) {
	all, err := e.dev.TaskList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]device.Task, 0, len(all))
	for _, task := range all {
		if task.DisplayID == displayID {
			out = append(out, task)
		}
	}
	return out, nil
}

// removeRecoveryTasks removes exact tasks on the recorded display in bounded
// sweeps. It never touches display 0.
func (e *Engine) removeRecoveryTasks(ctx context.Context, displayID int) (bool, error) {
	if displayID <= 0 {
		return false, nil
	}
	for sweep := 0; sweep < 3; sweep++ {
		tasks, err := e.tasksOnDisplayByTaskList(ctx, displayID)
		if err != nil {
			return false, err
		}
		if len(tasks) == 0 {
			return true, nil
		}
		for _, task := range tasks {
			if task.TaskID > 0 {
				_ = e.dev.RemoveTask(ctx, task.TaskID)
			}
		}
		timer := time.NewTimer(120 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		}
		timer.Stop()
	}
	tasks, err := e.tasksOnDisplayByTaskList(ctx, displayID)
	if err != nil {
		return false, err
	}
	return len(tasks) == 0, nil
}

// removeOwnedTasks removes every live task on the owned virtual display in at
// most three bounded sweeps, then verifies the live task list and RecentTasks
// index. It never touches display 0 or another session display.
func (e *Engine) removeOwnedTasks(ctx context.Context, displayID int, original []taskEntry) (tasksOK, recentsOK bool, checked []int) {
	if displayID <= 0 {
		return false, false, nil
	}
	removed := make(map[int]bool)
	for _, task := range original {
		if task.TaskID > 0 {
			removed[task.TaskID] = true
		}
	}
	defer func() {
		for id := range removed {
			checked = append(checked, id)
		}
		sort.Ints(checked)
	}()
	for sweep := 0; sweep < 3; sweep++ {
		tasks, known := e.dev.TasksOnDisplay(ctx, displayID)
		if !known {
			return false, false, nil
		}
		if len(tasks) == 0 {
			break
		}
		for _, task := range tasks {
			if task.TaskID <= 0 {
				continue
			}
			if !removed[task.TaskID] && len(removed) >= 256 {
				return false, false, nil
			}
			removed[task.TaskID] = true
			_ = e.dev.RemoveTask(ctx, task.TaskID)
		}
		timer := time.NewTimer(120 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false, false, nil
		}
		timer.Stop()
	}
	tasks, known := e.dev.TasksOnDisplay(ctx, displayID)
	if !known || len(tasks) > 0 {
		return false, false, nil
	}
	recent, recentsKnown := e.dev.RecentTaskIDs(ctx)
	if !recentsKnown {
		return true, false, nil
	}
	for id := range removed {
		if recent[id] {
			return true, false, nil
		}
	}
	return true, true, nil
}

func filterCommands(commands []*appCommand, target cleanupTarget) []*appCommand {
	out := commands[:0]
	for _, command := range commands {
		if command.AppInstance != target.appInstance {
			out = append(out, command)
			continue
		}
		if command.Kind == "create_display" {
			continue
		}
		if command.Kind == "close_display" && command.Command.TargetDisplayID != nil &&
			*command.Command.TargetDisplayID == target.display.ID {
			continue
		}
		out = append(out, command)
	}
	return out
}
