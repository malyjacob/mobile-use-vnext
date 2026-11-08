package engine

import (
	"context"
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
	mode         string
	appInstance  string
	display      device.Display
	createResult *bool
	createError  string
}

func targetOf(session *sessionState) cleanupTarget {
	target := cleanupTarget{
		epoch: session.Epoch, origin: session.Origin, mode: session.Mode,
		appInstance: session.AppInstanceID, display: session.Display,
		createResult: session.CreateResult, createError: session.CreateError,
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
	execUnknown := session.ExecutionUnknown ||
		(e.work != nil && e.work.SessionEpoch == session.Epoch && e.work.DispatchUnknown)
	cleanup := &cleanupState{
		State: protocol.CleanupPending, Execution: protocol.CleanupConfirmed,
		Tasks: protocol.CleanupPending, Recents: protocol.CleanupPending,
		Display: protocol.CleanupPending, Reason: reason, StartedAt: time.Now(),
	}
	if execUnknown {
		cleanup.Execution = protocol.CleanupUnconfirmed
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

	// Tasks on the owned virtual display are the only tasks this session could
	// own; the first-loop tool set never launches applications, so a non-empty
	// list is unexpected and stays unconfirmed instead of being silently closed.
	tasks := e.tasksOnDisplay(ctx, displayID)
	if len(tasks) > 0 {
		cleanup.Tasks = protocol.CleanupUnconfirmed
		cleanup.Recents = protocol.CleanupUnconfirmed
		cleanup.Error = &protocol.Error{Code: "owned_tasks_present",
			Message: "原虚拟屏仍存在未清理的任务"}
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
	}
	complete := result.Execution != protocol.CleanupUnconfirmed &&
		result.Display != protocol.CleanupUnconfirmed &&
		result.Tasks != protocol.CleanupUnconfirmed &&
		result.Recents != protocol.CleanupUnconfirmed &&
		result.Display != protocol.CleanupPending &&
		result.Tasks != protocol.CleanupPending &&
		result.Recents != protocol.CleanupPending
	if complete {
		result.State = protocol.CleanupComplete
	} else {
		result.State = protocol.CleanupUnconfirmed
	}
	*cleanup = result
	if cleanup.done != nil {
		close(cleanup.done)
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
