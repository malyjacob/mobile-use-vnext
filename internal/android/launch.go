package android

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// App launch and task primitives. They only perform bounded device I/O and
// return controlled facts; the engine decides whether a launched task belongs
// to the session and how it is cleaned up.

const (
	launchTimeout = 20 * time.Second
	taskTimeout   = 8 * time.Second
	// launchFlags is NEW_TASK | MULTIPLE_TASK: the target virtual display has no
	// launcher task of its own, so the new window must be a root task.
	launchFlags = "0x18000000"
)

var launcherComponent = regexp.MustCompile(`^[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*/[A-Za-z0-9_.$]+$`)

// ResolveLauncher resolves the single MAIN/LAUNCHER activity of a package.
// found=false means the package has no unique launchable launcher entry; a
// transport/integration failure is returned separately.
func (d *Device) ResolveLauncher(ctx context.Context, pkg string) (string, bool, error) {
	if d == nil {
		return "", false, errNilDevice
	}
	if !validPackageName(pkg) {
		return "", false, newErr(CodeInvalidArgs, "invalid package name", nil)
	}
	command := "cmd package resolve-activity --brief --user 0" +
		" -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -p " + shellQuote(pkg)
	result, err := d.shellWith(ctx, taskTimeout, command)
	if err != nil {
		return "", false, err
	}
	if result.ExitCode != 0 {
		return "", false, nil
	}
	var component string
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "priority=") {
			continue
		}
		component = line
	}
	if !launcherComponent.MatchString(component) || !strings.HasPrefix(component, pkg+"/") {
		return "", false, nil
	}
	return component, true, nil
}

// LaunchApp starts the resolved component on the target display. Completion of
// the am command is not proof that a task exists; the caller verifies the live
// task list and foreground record.
func (d *Device) LaunchApp(ctx context.Context, displayID int, component string) (device.LaunchResult, error) {
	if d == nil {
		return device.LaunchResult{
			State: device.LaunchNotStarted, Receipt: device.DispatchNotDispatched,
		}, errNilDevice
	}
	if displayID < 0 {
		return device.LaunchResult{
			State: device.LaunchNotStarted, Receipt: device.DispatchNotDispatched,
		}, newErr(CodeInvalidArgs, "invalid display id", nil)
	}
	if !launcherComponent.MatchString(component) {
		return device.LaunchResult{
			State: device.LaunchNotStarted, Receipt: device.DispatchNotDispatched,
		}, newErr(CodeInvalidArgs, "invalid launch component", nil)
	}
	command := "am start -W --display " + strconv.Itoa(displayID) +
		" -n " + component + " -f " + launchFlags
	result, receipt, err := d.runShellV2(ctx, launchTimeout, command)
	if err != nil {
		state := device.LaunchUnknown
		if receipt == device.DispatchNotDispatched {
			state = device.LaunchNotStarted
		}
		return device.LaunchResult{State: state, Receipt: receipt}, err
	}
	if receipt != device.DispatchTerminated {
		return device.LaunchResult{State: device.LaunchUnknown, Receipt: receipt}, nil
	}
	if result.ExitCode != 0 {
		return device.LaunchResult{
			State: device.LaunchRejected, ExitCode: result.ExitCode,
			Receipt: device.DispatchTerminated,
		}, nil
	}
	text := string(result.Stdout)
	if strings.Contains(text, "Error:") || strings.Contains(text, "Permission Denial") {
		return device.LaunchResult{
			State: device.LaunchRejected, Receipt: device.DispatchTerminated,
		}, nil
	}
	if strings.Contains(text, "Status: ok") {
		return device.LaunchResult{
			State: device.LaunchAccepted, Receipt: device.DispatchTerminated,
		}, nil
	}
	return device.LaunchResult{
		State: device.LaunchUnknown, Receipt: device.DispatchTerminated,
	}, nil
}

// FocusTask brings one live leaf task to the front on its own display.
func (d *Device) FocusTask(ctx context.Context, taskID int) (device.DispatchReceipt, error) {
	if d == nil {
		return device.DispatchNotDispatched, errNilDevice
	}
	if taskID <= 0 {
		return device.DispatchNotDispatched, newErr(CodeInvalidArgs, "invalid task id", nil)
	}
	result, receipt, err := d.runShellV2(ctx, taskTimeout, "am task focus "+strconv.Itoa(taskID))
	if err != nil {
		return receipt, err
	}
	if result.ExitCode != 0 {
		return receipt, exitDetail(result.ExitCode)
	}
	return receipt, nil
}

// RemoveTask removes one exact leaf task and its RecentTasks entry. The receipt
// describes the command boundary only: a device status other than zero proves
// the request finished but never proves the task ended, so it is reported as an
// unconfirmed dispatch with a controlled error. The command is never retried.
func (d *Device) RemoveTask(ctx context.Context, taskID int) (device.TaskRemoveResult, error) {
	result := device.TaskRemoveResult{Receipt: device.DispatchNotDispatched, TaskID: taskID}
	if d == nil {
		return result, errNilDevice
	}
	if taskID <= 0 {
		return result, newErr(CodeInvalidArgs, "invalid task id", nil)
	}
	started := time.Now()
	shell, receipt, err := d.runShellV2(ctx, taskTimeout, "am stack remove "+strconv.Itoa(taskID))
	result.Receipt = receipt
	if err != nil {
		d.logRemove(receipt, started)
		return result, err
	}
	if receipt != device.DispatchTerminated {
		// A returned command whose end could not be confirmed stays unknown.
		result.Receipt = device.DispatchUnknown
		d.logRemove(result.Receipt, started)
		return result, nil
	}
	if shell.ExitCode != 0 {
		// The removal itself is unconfirmed; never report it as terminated.
		result.Receipt = device.DispatchUnknown
		d.logRemove(result.Receipt, started)
		return result, exitDetail(shell.ExitCode)
	}
	d.logRemove(result.Receipt, started)
	return result, nil
}

// logRemove records the removal dispatch boundary. Only whitelisted fields are
// written: no package, task text or device output.
func (d *Device) logRemove(receipt device.DispatchReceipt, started time.Time) {
	d.logEvent(eventlog.Info, "android.task.remove",
		eventlog.Str("state", receiptName(receipt)),
		eventlog.Duration("ms", time.Since(started)))
}

// receiptName renders one dispatch receipt as a controlled token.
func receiptName(receipt device.DispatchReceipt) string {
	switch receipt {
	case device.DispatchTerminated:
		return "terminated"
	case device.DispatchNotDispatched:
		return "not_dispatched"
	default:
		return "unknown"
	}
}

// MoveTask moves one verified leaf task's root task to another display with
// `am display move-stack`. The adapter validates only the command arguments; the
// engine owns the candidate, ownership, root/leaf and landing checks and never
// lets this primitive retry. A non-zero device status is reported as an
// unconfirmed move because it never proves the task moved.
//
// SourceDisplayID is not part of the dispatch boundary and the frozen signature
// carries no source argument, so the result leaves it zero: the adapter never
// guesses where the task came from.
func (d *Device) MoveTask(ctx context.Context, rootID, leafTaskID, targetDisplayID int) (
	device.TaskMoveResult, error,
) {
	result := device.TaskMoveResult{
		Receipt:         device.DispatchNotDispatched,
		RootID:          rootID,
		LeafTaskID:      leafTaskID,
		TargetDisplayID: targetDisplayID,
	}
	if d == nil {
		return result, errNilDevice
	}
	if rootID <= 0 || leafTaskID <= 0 || targetDisplayID < 0 {
		return result, newErr(CodeInvalidArgs, "invalid task move target", nil)
	}
	started := time.Now()
	command := "am display move-stack " + strconv.Itoa(rootID) + " " + strconv.Itoa(targetDisplayID)
	shell, receipt, err := d.runShellV2(ctx, taskTimeout, command)
	result.Receipt = receipt
	if err != nil {
		d.logMove(targetDisplayID, receipt, started)
		return result, err
	}
	if receipt != device.DispatchTerminated {
		result.Receipt = device.DispatchUnknown
		d.logMove(targetDisplayID, result.Receipt, started)
		return result, nil
	}
	if shell.ExitCode != 0 {
		result.Receipt = device.DispatchUnknown
		d.logMove(targetDisplayID, result.Receipt, started)
		return result, exitDetail(shell.ExitCode)
	}
	d.logMove(targetDisplayID, result.Receipt, started)
	return result, nil
}

// logMove records the move dispatch boundary with whitelisted fields only.
func (d *Device) logMove(targetDisplayID int, receipt device.DispatchReceipt, started time.Time) {
	d.logEvent(eventlog.Info, "android.task.move",
		eventlog.Int("display_id", targetDisplayID),
		eventlog.Str("state", receiptName(receipt)),
		eventlog.Duration("ms", time.Since(started)))
}

// RecentTaskIDs reads the RecentTasks index. It is a separate fact from the
// live task list: a dead task can leave an orphan recents entry.
func (d *Device) RecentTaskIDs(ctx context.Context) (map[int]bool, bool) {
	result := d.ReadRecentTasks(ctx)
	return result.IDs, result.State == "known_empty" || result.State == "known_present"
}

// RecentTasksResult separates an unreadable index from a readable empty one.
// State is controlled metadata; raw dumpsys output never leaves this adapter.
func (d *Device) ReadRecentTasks(ctx context.Context) device.RecentTasksResult {
	if d == nil {
		return device.RecentTasksResult{State: "transport_unavailable"}
	}
	result, err := d.shellWith(ctx, taskTimeout, "dumpsys activity recents")
	if err != nil {
		return device.RecentTasksResult{State: "transport_unavailable"}
	}
	if result.ExitCode != 0 {
		return device.RecentTasksResult{State: "nonzero_exit"}
	}
	return parseRecentTasks(result.Stdout)
}

func parseRecentTasks(output []byte) device.RecentTasksResult {
	text := string(output)
	matches := recentTaskPattern.FindAllSubmatch(output, -1)
	if strings.Count(text, "Recent #") != len(matches) {
		return device.RecentTasksResult{State: "format_unrecognized"}
	}
	if len(matches) == 0 && !strings.Contains(text, "ACTIVITY MANAGER RECENT TASKS") && !strings.Contains(text, "Recent tasks") {
		return device.RecentTasksResult{State: "format_unrecognized"}
	}
	ids := make(map[int]bool)
	for _, match := range matches {
		id, err := strconv.Atoi(string(match[1]))
		if err == nil && id > 0 {
			ids[id] = true
		}
	}
	state := "known_empty"
	if len(ids) > 0 {
		state = "known_present"
	}
	return device.RecentTasksResult{IDs: ids, State: state}
}

// recentTaskPattern matches the task id in one RecentTasks record.
var recentTaskPattern = regexp.MustCompile(`(?m)^\s*\*?\s*Recent #\d+: Task\{[^}]*#(\d+)`)

// TasksOnDisplay returns the live tasks currently hosted by one display.
func (d *Device) TasksOnDisplay(ctx context.Context, displayID int) ([]device.Task, bool) {
	if d == nil {
		return nil, false
	}
	tasks, err := d.TaskList(ctx)
	if err != nil {
		return nil, false
	}
	out := make([]device.Task, 0, len(tasks))
	for _, task := range tasks {
		if task.DisplayID == displayID {
			out = append(out, task)
		}
	}
	return out, true
}
