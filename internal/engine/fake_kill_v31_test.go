package engine

import (
	"context"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// Contract 31 package-lifecycle fakes. The task snapshot and process inventory
// scripts are consumed one step per read; once a script is exhausted the
// mutable base state is returned. Package identity defaults to a stable
// installation for the requested package/user unless a test overrides it.

type taskSnapshotStep struct {
	tasks    []device.Task
	complete bool
	err      error
}

type processStep struct {
	processes device.PackageProcesses
	fail      bool
	err       error
}

type forceStopCall struct {
	pkg    string
	userID int
}

type packageIdentityCall struct {
	pkg    string
	userID int
}

const fakeKillBootID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

// TaskSnapshot returns the scripted or current task inventory.
func (f *fakeDevice) TaskSnapshot(context.Context) (device.TaskSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskSnapshotCalls++
	if len(f.taskSnapshotScript) > 0 {
		step := f.taskSnapshotScript[0]
		f.taskSnapshotScript = f.taskSnapshotScript[1:]
		if step.err != nil {
			return device.TaskSnapshot{}, step.err
		}
		return device.TaskSnapshot{
			Tasks:    append([]device.Task(nil), step.tasks...),
			Complete: step.complete,
		}, nil
	}
	if f.taskSnapshotErr != nil {
		return device.TaskSnapshot{}, f.taskSnapshotErr
	}
	return device.TaskSnapshot{
		Tasks:    append([]device.Task(nil), f.tasks...),
		Complete: !f.taskSnapshotIncomplete,
	}, nil
}

// PackageIdentity returns the scripted or default installation identity.
func (f *fakeDevice) PackageIdentity(_ context.Context, pkg string, androidUserID int) (device.PackageIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.packageIdentityCalls = append(f.packageIdentityCalls,
		packageIdentityCall{pkg: pkg, userID: androidUserID})
	if len(f.packageIdentityScript) > 0 {
		identity := f.packageIdentityScript[0]
		f.packageIdentityScript = f.packageIdentityScript[1:]
		return identity, nil
	}
	if f.packageIdentityErr != nil {
		return device.PackageIdentity{}, f.packageIdentityErr
	}
	if f.packageIdentityUnknown {
		return device.PackageIdentity{}, nil
	}
	if f.packageIdentity != nil {
		identity := *f.packageIdentity
		if identity.Package == "" {
			identity.Package = pkg
		}
		if identity.AndroidUserID == 0 && androidUserID != 0 {
			identity.AndroidUserID = androidUserID
		}
		return identity, nil
	}
	const appID = 10123
	return device.PackageIdentity{
		Known: true, Package: pkg, AndroidUserID: androidUserID,
		AppID: appID, AppUID: androidUserID*100000 + appID,
		FirstInstallTime: "2026-08-04 19:53:47", SignatureHash: "8f53736c",
	}, nil
}

// ForceStopPackage records the one package-level dispatch. A successful
// default dispatch also models Android removing the package's tasks and
// processes for that user.
func (f *fakeDevice) ForceStopPackage(_ context.Context, pkg string, androidUserID int) (device.ForceStopResult, error) {
	f.mu.Lock()
	f.forceStopCalls = append(f.forceStopCalls,
		forceStopCall{pkg: pkg, userID: androidUserID})
	receipt := device.DispatchTerminated
	if f.forceStopReceipt != nil {
		receipt = *f.forceStopReceipt
	}
	result := device.ForceStopResult{Receipt: receipt}
	if receipt == device.DispatchTerminated {
		result.ExitCode = f.forceStopExitCode
		if f.forceStopExitCodeSet {
			result.HasExitCode = true
		} else {
			result.ExitCode = 0
			result.HasExitCode = true
		}
	}
	apply := receipt == device.DispatchTerminated && result.ExitCode == 0 &&
		f.forceStopErr == nil && !f.forceStopNoop
	if apply {
		kept := f.tasks[:0]
		for _, task := range f.tasks {
			if task.Package == pkg && (!task.HasUserID || task.UserID == androidUserID) {
				continue
			}
			kept = append(kept, task)
		}
		f.tasks = kept
		f.processes.Instances = []device.ProcessInstance{}
	}
	hook := f.forceStopHook
	err := f.forceStopErr
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return result, err
}
