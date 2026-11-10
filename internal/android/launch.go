package android

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
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

// RemoveTask removes one leaf task and its RecentTasks entry.
func (d *Device) RemoveTask(ctx context.Context, taskID int) error {
	if d == nil {
		return errNilDevice
	}
	if taskID <= 0 {
		return newErr(CodeInvalidArgs, "invalid task id", nil)
	}
	result, err := d.shellWith(ctx, taskTimeout, "am stack remove "+strconv.Itoa(taskID))
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return newErr(CodeAvailability, "the task could not be removed", nil)
	}
	return nil
}

// RecentTaskIDs reads the RecentTasks index. It is a separate fact from the
// live task list: a dead task can leave an orphan recents entry.
func (d *Device) RecentTaskIDs(ctx context.Context) (map[int]bool, bool) {
	if d == nil {
		return nil, false
	}
	result, err := d.shellWith(ctx, taskTimeout, "dumpsys activity recents")
	if err != nil || result.ExitCode != 0 {
		return nil, false
	}
	ids := make(map[int]bool)
	for _, match := range recentTaskPattern.FindAllSubmatch(result.Stdout, -1) {
		id, convErr := strconv.Atoi(string(match[1]))
		if convErr == nil && id > 0 {
			ids[id] = true
		}
	}
	return ids, true
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
