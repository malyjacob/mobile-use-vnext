package engine

import (
	"context"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// App discovery and task primitives for the engine fake. They are kept in a
// separate file so the base fake remains readable.

func (f *fakeDevice) AppList(context.Context) ([]device.AppEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appCalls++
	if f.appErr != nil {
		return nil, f.appErr
	}
	return append([]device.AppEntry(nil), f.appEntries...), nil
}

func (f *fakeDevice) ResolveLauncher(_ context.Context, pkg string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resolveErr != nil {
		return "", false, f.resolveErr
	}
	if f.resolveMissing {
		return "", false, nil
	}
	if f.resolveComponent != "" {
		return f.resolveComponent, true, nil
	}
	return pkg + "/.MainActivity", true, nil
}

func (f *fakeDevice) LaunchApp(_ context.Context, displayID int, component string) (device.LaunchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launchCalls++
	if f.launchErr != nil {
		return device.LaunchResult{State: device.LaunchUnknown}, f.launchErr
	}
	state := f.launchState
	if state == "" {
		state = device.LaunchAccepted
	}
	if state == device.LaunchAccepted && f.launchTask != nil {
		task := *f.launchTask
		if task.DisplayID == 0 {
			task.DisplayID = displayID
		}
		f.tasks = append(f.tasks, task)
	}
	receipt := device.DispatchTerminated
	if state == device.LaunchUnknown {
		receipt = device.DispatchUnknown
	}
	if state == device.LaunchNotStarted {
		receipt = device.DispatchNotDispatched
	}
	return device.LaunchResult{State: state, Receipt: receipt}, nil
}

func (f *fakeDevice) FocusTask(_ context.Context, taskID int) (device.DispatchReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focusCalls = append(f.focusCalls, taskID)
	return device.DispatchTerminated, f.focusErr
}

func (f *fakeDevice) RecentTaskIDs(context.Context) (map[int]bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[int]bool, len(f.recentIDs))
	for id, present := range f.recentIDs {
		out[id] = present
	}
	return out, f.recentKnown
}

func (f *fakeDevice) TasksOnDisplay(_ context.Context, displayID int) ([]device.Task, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]device.Task, 0, len(f.tasks))
	for _, task := range f.tasks {
		if task.DisplayID == displayID {
			out = append(out, task)
		}
	}
	return out, true
}
