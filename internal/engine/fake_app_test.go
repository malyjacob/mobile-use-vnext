package engine

import (
	"context"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// moveCall records one dispatched task move.
type moveCall struct {
	rootID   int
	leafID   int
	targetID int
}

// MoveTask simulates the exact root-task move. The default receipt is a
// terminated dispatch; a test may override it or ask for no simulated move.
func (f *fakeDevice) MoveTask(_ context.Context, rootID, leafID, targetDisplayID int) (device.TaskMoveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moveCalls = append(f.moveCalls, moveCall{rootID: rootID, leafID: leafID, targetID: targetDisplayID})
	receipt := device.DispatchTerminated
	if f.moveReceipt != nil {
		receipt = *f.moveReceipt
	}
	if receipt == device.DispatchTerminated && f.moveErr == nil && !f.moveNoop {
		for i := range f.tasks {
			if f.tasks[i].TaskID == leafID {
				f.tasks[i].DisplayID = targetDisplayID
			}
		}
	}
	return device.TaskMoveResult{
		Receipt: receipt, RootID: rootID, LeafTaskID: leafID,
		TargetDisplayID: targetDisplayID,
	}, f.moveErr
}

// RemoveTask simulates one exact task removal.
func (f *fakeDevice) RemoveTask(_ context.Context, taskID int) (device.TaskRemoveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCalls = append(f.removeCalls, taskID)
	receipt := device.DispatchTerminated
	if len(f.removeScript) > 0 {
		receipt, f.removeScript = f.removeScript[0], f.removeScript[1:]
	} else if f.removeReceipt != nil {
		receipt = *f.removeReceipt
	}
	if receipt == device.DispatchTerminated && f.removeErr == nil {
		kept := f.tasks[:0]
		for _, task := range f.tasks {
			if task.TaskID == taskID {
				continue
			}
			kept = append(kept, task)
		}
		f.tasks = kept
		if f.recentIDs != nil {
			delete(f.recentIDs, taskID)
		}
	}
	return device.TaskRemoveResult{Receipt: receipt, TaskID: taskID}, f.removeErr
}

// PackageProcesses returns the scripted or current process inventory of one
// package for one Android user. An unset inventory is a known empty one.
func (f *fakeDevice) PackageProcesses(_ context.Context, pkg string, androidUserID int) (device.PackageProcesses, error) {
	f.mu.Lock()
	f.processCalls++
	call := f.processCalls
	if hook := f.processHook; hook != nil {
		defer func() { hook(call) }()
	}
	defer f.mu.Unlock()
	if len(f.processScript) > 0 {
		step := f.processScript[0]
		f.processScript = f.processScript[1:]
		if step.err != nil {
			return device.PackageProcesses{}, step.err
		}
		if step.fail {
			return device.PackageProcesses{Known: false, ReadFailed: true}, nil
		}
		result := step.processes
		result.Known = true
		result.ReadFailed = false
		if result.AndroidUserID == 0 {
			result.AndroidUserID = androidUserID
		}
		if result.BootID == "" {
			result.BootID = f.processes.BootID
			if result.BootID == "" {
				result.BootID = fakeKillBootID
			}
		}
		return result, nil
	}
	if f.processFail || f.processErr != nil {
		return device.PackageProcesses{Known: false, ReadFailed: true}, f.processErr
	}
	result := f.processes
	result.Known = true
	if result.AppUID == 0 {
		result.AppUID = 10123
	}
	result.AndroidUserID = androidUserID
	if result.BootID == "" {
		result.BootID = f.bootID
		if result.BootID == "" {
			result.BootID = fakeKillBootID
		}
	}
	if result.Instances == nil {
		result.Instances = []device.ProcessInstance{}
	}
	return result, nil
}

// KillProcess simulates one exact signal and removes the matching instance.
func (f *fakeDevice) KillProcess(_ context.Context, pid int, startTicks uint64) (device.DispatchReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killCalls = append(f.killCalls, pid)
	receipt := device.DispatchTerminated
	if f.killReceipt != nil {
		receipt = *f.killReceipt
	}
	if receipt == device.DispatchTerminated && f.killErr == nil {
		kept := f.processes.Instances[:0]
		for _, instance := range f.processes.Instances {
			if instance.PID == pid && instance.StartTicks == startTicks {
				continue
			}
			kept = append(kept, instance)
		}
		f.processes.Instances = kept
	}
	return receipt, f.killErr
}

// MediaPlayback returns the scripted media fact.
func (f *fakeDevice) MediaPlayback(context.Context) (device.MediaState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mediaErr != nil {
		return device.MediaState{}, f.mediaErr
	}
	if !f.media.Known && !f.media.Playing {
		return device.MediaState{Known: true}, nil
	}
	return f.media, nil
}

// ForegroundService returns the scripted foreground-service fact.
func (f *fakeDevice) ForegroundService(context.Context, string) (device.ServiceState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.serviceErr != nil {
		return device.ServiceState{}, f.serviceErr
	}
	if !f.service.Known && !f.service.Foreground {
		return device.ServiceState{Known: true}, nil
	}
	return f.service, nil
}
