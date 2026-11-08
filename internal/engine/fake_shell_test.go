package engine

import (
	"context"
	"sync"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// fakeShellHooks is the controllable managed-runner behavior of the engine
// fake. It is a separate value so the base fake stays readable.
type fakeShellHooks struct {
	mu sync.Mutex

	rootDenied bool
	rootErr    error
	rootCalls  int

	prepareErr   error
	prepareCalls int
	commands     [][]byte
	prepareHook  func()

	startErr      error
	start         device.ShellStart
	startCalls    int
	startRoots    []bool
	startDeadline []time.Duration

	inspectErr   error
	inspectCalls int
	hasStart     bool
	startInfo    device.ShellStart
	alive        bool
	hasDone      bool
	done         device.ShellDone

	output    []byte
	outputErr error
	readCalls int

	terminate        device.ShellTermination
	terminateErr     error
	terminateCalls   int
	terminateReasons []string
	terminateGate    <-chan struct{}

	removeErr   error
	removeCalls int
	runIDs      []string
}

// defaultStartIdentity is a stable runner identity for the common case.
func defaultShellStart() device.ShellStart {
	return device.ShellStart{
		HasIdentity: true, PID: 4242, PGID: 4242, StartTicks: 99, BootID: "boot-test",
	}
}

func (f *fakeDevice) RootAvailable(ctx context.Context) (bool, error) {
	f.shell.mu.Lock()
	defer f.shell.mu.Unlock()
	f.shell.rootCalls++
	if f.shell.rootErr != nil {
		return false, f.shell.rootErr
	}
	return !f.shell.rootDenied, nil
}

func (f *fakeDevice) PrepareManagedShell(ctx context.Context, runID string, command []byte) error {
	f.shell.mu.Lock()
	f.shell.prepareCalls++
	f.shell.commands = append(f.shell.commands, append([]byte(nil), command...))
	f.shell.runIDs = append(f.shell.runIDs, runID)
	err := f.shell.prepareErr
	hook := f.shell.prepareHook
	f.shell.mu.Unlock()
	if hook != nil {
		hook()
	}
	return err
}

func (f *fakeDevice) StartManagedShell(ctx context.Context, runID string, root bool,
	deadline time.Duration) (device.ShellStart, error) {
	f.shell.mu.Lock()
	defer f.shell.mu.Unlock()
	f.shell.startCalls++
	f.shell.startRoots = append(f.shell.startRoots, root)
	f.shell.startDeadline = append(f.shell.startDeadline, deadline)
	if f.shell.startErr != nil {
		return device.ShellStart{}, f.shell.startErr
	}
	start := f.shell.start
	if !start.HasIdentity {
		start = defaultShellStart()
	}
	f.shell.hasStart = true
	f.shell.startInfo = start
	f.shell.alive = true
	return start, nil
}

func (f *fakeDevice) InspectManagedShell(ctx context.Context, runID string) (device.ShellSnapshot, error) {
	f.shell.mu.Lock()
	f.shell.inspectCalls++
	if f.shell.inspectErr != nil {
		err := f.shell.inspectErr
		f.shell.mu.Unlock()
		return device.ShellSnapshot{}, err
	}
	snapshot := device.ShellSnapshot{
		HasStart: f.shell.hasStart, Start: f.shell.startInfo,
		HasDone: f.shell.hasDone, Done: f.shell.done,
		Alive: f.shell.alive && f.shell.hasStart,
	}
	f.shell.mu.Unlock()
	return snapshot, nil
}

func (f *fakeDevice) ReadManagedShellOutput(ctx context.Context, runID string) ([]byte, error) {
	f.shell.mu.Lock()
	defer f.shell.mu.Unlock()
	f.shell.readCalls++
	if f.shell.outputErr != nil {
		return nil, f.shell.outputErr
	}
	return append([]byte(nil), f.shell.output...), nil
}

func (f *fakeDevice) TerminateManagedShell(ctx context.Context, runID string, root bool,
	reason string) (device.ShellTermination, error) {
	f.shell.mu.Lock()
	f.shell.terminateCalls++
	f.shell.terminateReasons = append(f.shell.terminateReasons, reason)
	gate := f.shell.terminateGate
	f.shell.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return device.ShellTermination{Unknown: true}, ctx.Err()
		}
	}
	f.shell.mu.Lock()
	defer f.shell.mu.Unlock()
	if f.shell.terminateErr != nil {
		return device.ShellTermination{Unknown: true}, f.shell.terminateErr
	}
	return f.shell.terminate, nil
}

func (f *fakeDevice) RemoveManagedShell(ctx context.Context, runID string, root bool) error {
	f.shell.mu.Lock()
	defer f.shell.mu.Unlock()
	f.shell.removeCalls++
	return f.shell.removeErr
}

// ---------------------------------------------------------------- helpers

func (f *fakeDevice) setShellDone(done device.ShellDone) {
	f.shell.mu.Lock()
	defer f.shell.mu.Unlock()
	f.shell.hasDone, f.shell.done = true, done
}

func (f *fakeDevice) shellCalls() (prepare, start, inspect, read, terminate, remove int) {
	f.shell.mu.Lock()
	defer f.shell.mu.Unlock()
	return f.shell.prepareCalls, f.shell.startCalls, f.shell.inspectCalls,
		f.shell.readCalls, f.shell.terminateCalls, f.shell.removeCalls
}

func (f *fakeDevice) lastShellCommand() []byte {
	f.shell.mu.Lock()
	defer f.shell.mu.Unlock()
	if len(f.shell.commands) == 0 {
		return nil
	}
	return append([]byte(nil), f.shell.commands[len(f.shell.commands)-1]...)
}

func exitedDone(code int, bytes int, truncated bool) device.ShellDone {
	return device.ShellDone{
		Reason: terminationExited, ExitCode: &code,
		Bytes: bytes, HasBytes: true, Truncated: &truncated,
	}
}
