package engine

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// cleanupTarget freezes the original ownership used by the independent
// closure chain. It never carries user content.
type cleanupTarget struct {
	epoch         int64
	origin        string
	agent         string
	mode          string
	appInstance   string
	display       device.Display
	createCommand string
	identity      device.BootIdentity
	createResult  *bool
	createError   string
	tasks         []taskEntry
}

func targetOf(session *sessionState) cleanupTarget {
	tasks := append([]taskEntry(nil), session.Tasks...)
	if len(tasks) > 256 {
		tasks = tasks[:256]
	}
	target := cleanupTarget{
		epoch: session.Epoch, origin: session.Origin, agent: session.Agent,
		mode: session.Mode, appInstance: session.AppInstanceID, display: session.Display,
		createResult: session.CreateResult, createError: session.CreateError,
		tasks: tasks, createCommand: session.CreateCommand, identity: session.InputIdentity,
	}
	return target
}

// beginCleanupLocked creates the single pending closure record. A second
// caller only observes the existing chain.
func (e *Engine) beginCleanupLocked(session *sessionState, reason string) *cleanupState {
	if session == nil || session.CleanupStarted {
		return nil
	}
	execUnknown := e.uiPendingLocked() || e.shellUnprovenLocked() ||
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
	e.retireLocked(session, cleanup)
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
	if e.shellUnprovenLocked() {
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
	e.mu.Lock()
	r := e.retiredForCleanupLocked(cleanup)
	if r == nil || cleanup.State != protocol.CleanupPending {
		e.mu.Unlock()
		return
	}
	r.budgetIssued = true
	ctx, cancel := context.WithDeadline(context.Background(), cleanup.StartedAt.Add(e.cfg.CleanupTimeout))
	r.cancel = cancel
	e.mu.Unlock()
	if e.log != nil {
		e.log.Event(eventlog.Info, "cleanup.start", eventlog.Str("reason", cleanup.Reason),
			eventlog.Int("display_id", target.display.ID), eventlog.Str("mode", target.mode))
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer cancel()
		e.mu.Lock()
		result := *cleanup
		e.mu.Unlock()
		persistCtx, cancel := context.WithTimeout(context.Background(), e.cfg.LedgerTimeout)
		err := e.persistSync(persistCtx)
		cancel()
		if err != nil {
			result.Error = &protocol.Error{Code: "ledger_unavailable", Message: "原会话清理未能保存"}
		} else {
			e.mu.Lock()
			valid := e.retiredForCleanupLocked(cleanup) == r && cleanup.State == protocol.CleanupPending
			e.mu.Unlock()
			if valid && ctx.Err() == nil {
				e.performCleanup(ctx, target, &result)
			}
		}
		e.finishCleanup(target, cleanup, result)
	}()
}

// performCleanup performs the bounded physical closure. It never runs while
// holding the state lock and never revives input authority.
func (e *Engine) performCleanup(ctx context.Context, target cleanupTarget, cleanup *cleanupState) {
	e.mu.Lock()
	r := e.retiredForCleanupLockedByTarget(target)
	if r == nil || r.cleanup.State != protocol.CleanupPending {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	waits := []time.Duration{0, 200 * time.Millisecond, 800 * time.Millisecond}
	for attempt, wait := range waits {
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return
			}
			timer.Stop()
		}
		if ctx.Err() != nil {
			break
		}
		e.mu.Lock()
		if r != nil && e.retiredForCleanupLocked(r.cleanup) != r {
			e.mu.Unlock()
			return
		}
		busy := (e.work != nil && !e.work.Revoked) || e.maintenanceOwner != nil
		if r != nil {
			r.attempts = attempt + 1
		}
		ioCtx, ioCancel := context.WithCancel(ctx)
		if !busy {
			e.maintenanceCancel = ioCancel
			e.maintenanceOwner = cleanup
		}
		e.mu.Unlock()
		if busy {
			ioCancel()
			continue
		}
		if e.log != nil {
			e.log.Event(eventlog.Info, "cleanup.attempt", eventlog.Str("origin", target.origin), eventlog.Int("attempt", attempt+1))
		}
		cleanup.Error = nil
		if r != nil {
			e.resolveRetiredExecution(ioCtx, r)
		}

		var revision uint64
		if r != nil {
			e.mu.Lock()
			revision = r.cleanup.evidenceRevision
			cleanup.Execution = r.cleanup.Execution
			e.mu.Unlock()
		}
		e.performCleanupAttempt(ioCtx, target, cleanup)
		if r != nil {
			e.mu.Lock()
			cleanup.needsExecutionRecheck = r.cleanup.evidenceRevision != revision
			cleanup.evidenceRevision = revision
			e.mu.Unlock()
		}
		ioCancel()
		e.mu.Lock()
		if e.maintenanceOwner == cleanup {
			e.maintenanceCancel = nil
			e.maintenanceOwner = nil
		}
		e.mu.Unlock()
		if closureComplete(cleanup) {
			return
		}
	}
	if cleanup.Error == nil {
		cleanup.Error = &protocol.Error{Code: "cleanup_budget_exhausted", Message: "原会话自动清理预算已结束"}
	}
}

func (e *Engine) retiredForCleanupLockedByTarget(target cleanupTarget) *retiredState {
	for _, r := range e.retired {
		if r.session.Epoch == target.epoch && r.session.Origin == target.origin {
			return r
		}
	}
	return nil
}

func (e *Engine) performCleanupAttempt(ctx context.Context, target cleanupTarget, cleanup *cleanupState) {
	if target.mode != "virtual" {
		return
	}
	displayID, generation := target.display.ID, target.display.Generation
	if displayID == 0 {
		e.mu.Lock()
		if e.app.InstanceID == target.appInstance && e.app.Display != nil && e.app.Display.Generation == target.createCommand {
			displayID, generation = e.app.Display.ID, e.app.Display.Generation
		}
		e.mu.Unlock()
		if displayID == 0 {
			cleanup.Tasks, cleanup.Recents = protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed
			cleanup.Display = protocol.CleanupUnconfirmed
			if target.createResult != nil && !*target.createResult && (target.createError == "display_dimensions_invalid" || target.createError == "display_arguments_invalid") {
				cleanup.Tasks, cleanup.Recents, cleanup.Display = protocol.CleanupNotApplicable, protocol.CleanupNotApplicable, protocol.CleanupConfirmed
			}
			return
		}
	}
	target.display.ID, target.display.Generation = displayID, generation
	e.mu.Lock()
	if r := e.retiredForCleanupLockedByTarget(target); r != nil && r.session.Display.ID == 0 {
		r.session.Display.ID = displayID
		r.session.Display.Generation = generation
	}
	conflict := e.cleanupDisplayConflictLocked(target.epoch, displayID)
	appInstance := e.app.InstanceID
	e.mu.Unlock()
	if conflict {
		cleanup.Tasks, cleanup.Recents, cleanup.Display = protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed
		cleanup.Error = &protocol.Error{Code: "cleanup_identity_conflict", Message: "旧清理目标与当前归属冲突"}
		return
	}
	identity, identityErr := e.cleanupIdentity(ctx)
	destructive := identityErr == nil && validUIIdentity(target.identity) && identity == target.identity
	if destructive {
		tasksOK, recentsOK, ids := e.removeOwnedTasksIdentity(ctx, displayID, cleanupTasksOfTarget(target, cleanup), target.identity, target.epoch)
		cleanup.checkedTaskIDs = ids
		cleanup.Tasks, cleanup.Recents = protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed
		if tasksOK {
			cleanup.Tasks = protocol.CleanupConfirmed
		}
		if tasksOK && len(ids) == 0 {
			cleanup.Recents = protocol.CleanupNotApplicable
		} else if recentsOK {
			cleanup.Recents = protocol.CleanupConfirmed
		} else {
			cleanup.Error = &protocol.Error{Code: "recents_cleanup_unconfirmed", Message: "最近任务清理尚未确认"}
		}
	} else {
		ids := append([]int(nil), cleanup.checkedTaskIDs...)
		for _, t := range target.tasks {
			ids = append(ids, t.TaskID)
		}
		cleanup.Tasks, cleanup.Recents, _ = e.verifyClosedExecutionTarget(ctx, target, ids)
	}
	// Closing is bound by original App/create generation, even if a numerical
	// display ID is reused. Task deletion still needs a same-boot identity.
	if target.appInstance == appInstance && generation != "" {
		e.enqueueCloseCommand(displayID, generation, appInstance)
	}
	if cleanup.Display != protocol.CleanupConfirmed {
		cleanup.Display = protocol.CleanupUnconfirmed
	}
	if absent, known := e.displayConfirmedAbsent(ctx, displayID); known && absent {
		cleanup.Display = protocol.CleanupConfirmed
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
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
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
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
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
	r := e.retiredForCleanupLocked(cleanup)
	if e.cleanup != cleanup && r == nil {
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
	result.needsExecutionRecheck = result.needsExecutionRecheck || (r == nil && result.evidenceRevision != e.executionEvidenceRevision) ||
		(r != nil && result.evidenceRevision != cleanup.evidenceRevision)
	if result.needsExecutionRecheck && target.mode == "virtual" {
		result.Tasks, result.Recents, result.Display = protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed, protocol.CleanupUnconfirmed
	}
	complete := !result.needsExecutionRecheck && (r != nil || !e.uiPendingLocked()) && result.Execution != protocol.CleanupUnconfirmed &&
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
	if !complete {
		stopCleanup(cleanup, "cleanup_budget_exhausted")
	}
	if r != nil {
		r.stoppedReason = "cleanup_budget_exhausted"
		if complete {
			r.stoppedReason = "complete"
		}
	}
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
	}
	e.commands = filterCommands(e.commands, target)
	e.detachRetiredLocked(time.Now())
	e.broadcastAppLocked()
	logged := *cleanup
	e.mu.Unlock()
	if e.log != nil {
		fields := []eventlog.Field{
			eventlog.Str("state", logged.State),
			eventlog.Str("execution", logged.Execution),
			eventlog.Str("tasks", logged.Tasks),
			eventlog.Str("recents", logged.Recents),
			eventlog.Str("display", logged.Display),
			eventlog.Str("reason", logged.Reason),
		}
		if complete {
			e.log.Event(eventlog.Info, "cleanup.complete", fields...)
		} else {
			e.log.Event(eventlog.Warn, "cleanup.unconfirmed", fields...)
			e.log.Event(eventlog.Info, "cleanup.stopped", eventlog.Str("origin", target.origin), eventlog.Str("state", logged.State))
		}
	}
	e.persistAsync()
	e.launchExecutionCleanupRecheck()
}

// tasksOnDisplayByTaskList reads the complete task list and filters one
// display. Recovery uses this path so an unreadable list stays unknown.
func (e *Engine) tasksOnDisplayByTaskList(ctx context.Context, displayID int) ([]device.Task, error) {
	all, err := e.cleanupTaskList(ctx)
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
			if task.TaskID > 0 && !e.transferProtected(task.TaskID) {
				_, _ = e.dev.RemoveTask(ctx, task.TaskID)
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
	return e.removeOwnedTasksIdentity(ctx, displayID, original, device.BootIdentity{}, 0)
}

func (e *Engine) removeOwnedTasksIdentity(ctx context.Context, displayID int, original []taskEntry, identity device.BootIdentity, epoch int64) (tasksOK, recentsOK bool, checked []int) {
	if displayID <= 0 {
		return false, false, nil
	}
	owned := make(map[int]bool)
	for _, t := range original {
		if t.TaskID > 0 && t.Proven && t.DisplayID == displayID {
			owned[t.TaskID] = true
		}
	}
	defer func() {
		for id := range owned {
			checked = append(checked, id)
		}
		sort.Ints(checked)
	}()
	all, err := e.cleanupTaskList(ctx)
	if err != nil {
		return false, false, nil
	}
	live := map[int]int{}
	for _, t := range all {
		live[t.TaskID] = t.DisplayID
		if t.DisplayID == displayID && t.TaskID > 0 {
			if e.transferProtected(t.TaskID) {
				// A handed-off or in-flight moved window is never adopted back
				// into the delete list by a local live scan.
				continue
			}
			if len(owned) >= 256 && !owned[t.TaskID] {
				return false, false, nil
			}
			owned[t.TaskID] = true
		}
	}
	recent, known := e.readCleanupRecents(ctx)
	for id := range owned {
		if ctx.Err() != nil {
			return false, false, nil
		}
		if place, present := live[id]; present && place != displayID {
			return false, false, nil
		}
		if e.transferProtected(id) {
			// An unresolved or released transfer still protects the exact id.
			continue
		}
		if _, present := live[id]; !present && (!known || !recent[id]) {
			continue
		}
		e.mu.Lock()
		conflict := e.cleanupDisplayConflictLocked(epoch, displayID) || e.cleanupTaskConflictLocked(epoch, id)
		e.mu.Unlock()
		if conflict {
			return false, false, nil
		}
		// Re-read immediately before each destructive dispatch: an old task
		// moved to the user's primary display must not be deleted by ID.
		latest, err := e.cleanupTaskList(ctx)
		if err != nil {
			return false, false, nil
		}
		for _, t := range latest {
			if t.TaskID == id && t.DisplayID != displayID {
				return false, false, nil
			}
		}
		if validUIIdentity(identity) {
			current, err := e.cleanupIdentity(ctx)
			if err != nil || current != identity {
				return false, false, nil
			}
		}
		if ctx.Err() != nil {
			return false, false, nil
		}
		e.mu.Lock()
		conflict = e.cleanupDisplayConflictLocked(epoch, displayID) || e.cleanupTaskConflictLocked(epoch, id) || !e.cleanupOwnerValidLocked(epoch)
		e.mu.Unlock()
		if conflict {
			return false, false, nil
		}
		_ = e.cleanupRemoveTask(ctx, id)
	}
	tasks, known := e.cleanupLiveTasks(ctx, displayID)
	if !known || len(tasks) > 0 {
		return false, false, nil
	}
	recent, known = e.readCleanupRecents(ctx)
	if !known {
		return true, false, nil
	}
	for id := range owned {
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
		if command.Kind == "cancel_create" && command.Command.TargetCreateCommandID == target.createCommand {
			continue
		}
		if command.Kind == "create_display" && command.Command.ID == target.createCommand {
			continue
		}
		if command.Kind == "close_display" && command.Command.TargetDisplayID != nil &&
			*command.Command.TargetDisplayID == target.display.ID && command.Command.TargetGeneration == target.display.Generation {
			continue
		}
		out = append(out, command)
	}
	return out
}

func cleanupTasksOfTarget(target cleanupTarget, c *cleanupState) []taskEntry {
	tasks := append([]taskEntry(nil), target.tasks...)
	for _, id := range c.checkedTaskIDs {
		tasks = append(tasks, taskEntry{TaskID: id, DisplayID: target.display.ID, Proven: true})
	}
	return tasks
}

func (e *Engine) cleanupDisplayConflictLocked(epoch int64, displayID int) bool {
	if e.session != nil && e.session.State != SessionEnded && e.session.Epoch != epoch && e.session.Display.ID == displayID {
		return true
	}
	for _, r := range e.retired {
		if r.session.Epoch != epoch && r.session.Display.ID == displayID && r.cleanup.Display != protocol.CleanupConfirmed && r.cleanup.Display != protocol.CleanupNotApplicable {
			return true
		}
	}
	return false
}

func (e *Engine) cleanupTaskConflictLocked(epoch int64, id int) bool {
	if e.session != nil && e.session.State != SessionEnded && e.session.Epoch != epoch {
		for _, t := range e.session.Tasks {
			if t.TaskID == id {
				return true
			}
		}
	}
	for _, r := range e.retired {
		if r.session.Epoch != epoch && r.cleanup.State != protocol.CleanupComplete {
			for _, t := range r.session.Tasks {
				if t.TaskID == id {
					return true
				}
			}
		}
	}
	return false
}

func (e *Engine) readCleanupRecents(ctx context.Context) (map[int]bool, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if reader, ok := e.dev.(interface {
		ReadRecentTasks(context.Context) device.RecentTasksResult
	}); ok {
		result := reader.ReadRecentTasks(ctx)
		known := result.State == "known_empty" || result.State == "known_present"
		if !known && e.log != nil {
			e.log.Event(eventlog.Warn, "cleanup.recents_read", eventlog.Str("phase", "recents"), eventlog.Str("code", result.State))
		}
		return result.IDs, known
	}
	return e.dev.RecentTaskIDs(ctx)
}

func (e *Engine) cleanupIdentity(ctx context.Context) (device.BootIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return e.dev.ExecutionIdentity(ctx)
}
func (e *Engine) cleanupTaskList(ctx context.Context) ([]device.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return e.dev.TaskList(ctx)
}
func (e *Engine) cleanupLiveTasks(ctx context.Context, id int) ([]device.Task, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return e.dev.TasksOnDisplay(ctx, id)
}
func (e *Engine) cleanupRemoveTask(ctx context.Context, id int) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := e.dev.RemoveTask(ctx, id)
	if err != nil {
		return err
	}
	if result.Receipt != device.DispatchTerminated {
		return errTaskRemoveUnconfirmed
	}
	return nil
}

// errTaskRemoveUnconfirmed reports a removal whose dispatch end was not proven.
var errTaskRemoveUnconfirmed = errors.New("task removal unconfirmed")

// transferProtected reports whether a transfer record forbids deleting this id.
func (e *Engine) transferProtected(taskID int) bool {
	if taskID <= 0 {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.transferProtectsLocked(taskID)
}

func (e *Engine) cleanupOwnerValidLocked(epoch int64) bool {
	if epoch == 0 {
		return true
	}
	for _, r := range e.retired {
		if r.session.Epoch == epoch {
			return r.cleanup.State == protocol.CleanupPending && r.session.AppInstanceID == e.app.InstanceID && e.appFreshLocked(time.Now()) &&
				(e.app.Display == nil || e.app.Display.ID != r.session.Display.ID || e.app.Display.Generation == r.session.Display.Generation)
		}
	}
	return false
}
