package android

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// serverProcess is the local adb shell that keeps one u2 app_process alive.
//
// The device process is the child of the device shell; the local adb process
// must stay running for the server's lifetime. Killing the local process closes
// the ADB shell channel and the device-side app_process sees EOF and exits.
type serverProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *boundedBuffer
	stderr *boundedBuffer

	done chan struct{}
	err  error

	mu     sync.Mutex
	closed bool
}

// start launches the server shell. The returned process is owned by the Device
// and must be released by stopServer.
func (d *Device) startServerProcess(ctx context.Context, exe string, timeout time.Duration) (*serverProcess, error) {
	command := "CLASSPATH=" + d.remoteJarPath + " app_process / " + u2MainClass
	// The server must outlive the preparation call that started it: tying the
	// long-lived adb shell to a caller deadline would kill the ready server as
	// soon as EnsureU2 returns. StopU2 owns the explicit lifetime.
	runCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(runCtx, exe, d.adbArgs("shell", command)...)
	if len(d.testEnv) > 0 {
		cmd.Env = append(os.Environ(), d.testEnv...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, newErr(CodeAvailability, "the server shell could not be started", nil)
	}
	outBuf := &boundedBuffer{limit: 64 << 10}
	errBuf := &boundedBuffer{limit: 64 << 10}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		return nil, execFailure(err, false)
	}
	process := &serverProcess{
		cmd: cmd, stdin: stdin, stdout: outBuf, stderr: errBuf,
		done: make(chan struct{}),
	}
	go func() {
		process.err = cmd.Wait()
		cancel()
		close(process.done)
	}()
	return process, nil
}

// String redacts the argv; only the process identity is ever logged.
func (p *serverProcess) String() string { return "u2-server-process" }

// exitStatus reports whether the server process already ended.
func (p *serverProcess) exitStatus() (bool, error) {
	select {
	case <-p.done:
		return true, p.err
	default:
		return false, nil
	}
}

// stop closes stdin first so the device shell gets EOF and returns as soon as
// the local adb process exits. A process that does not exit within the bounded
// killAfter window is killed, then reaped.
func (p *serverProcess) stop(killAfter time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	stdin := p.stdin
	p.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	select {
	case <-p.done:
		return
	case <-time.After(killAfter):
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
}

// waitReady polls /ping until the server answers, the process exits, or the
// deadline passes. Server stdout is inspected only for the documented
// "already registered" condition and is never logged.
func (d *Device) waitReady(ctx context.Context, process *serverProcess, deadline time.Duration) error {
	end := time.Now().Add(deadline)
	for {
		if d.ping(ctx, d.client, d.endpoint()) {
			return nil
		}
		if process != nil {
			if exited, _ := process.exitStatus(); exited || process.stdout.overflow ||
				bytes.Contains(process.stdout.Bytes(), []byte("already registered")) {
				return newErr(CodeServerStart, "the u2 server exited during startup", nil)
			}
		}
		if time.Now().After(end) {
			return newErr(CodeServerStart, "the u2 server did not become ready in time", nil)
		}
		select {
		case <-ctx.Done():
			return contextError(ctx)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// EnsureU2 verifies the local JAR hash, stops any old u2 app_process, pushes
// the fixed JAR when needed, starts the server and waits for /ping readiness.
// A started server must be stopped explicitly by StopU2.
func (d *Device) EnsureU2(ctx context.Context) error {
	if d == nil {
		return errNilDevice
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	started := time.Now()
	if _, err := verifyLocalJar(d.localJarPath); err != nil {
		d.logEvent(eventlog.Warn, "android.u2.artifact_rejected", eventlog.Str("code", CodeOf(err)))
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.server != nil {
		if exited, _ := d.server.exitStatus(); !exited {
			if d.ping(ctx, d.client, d.endpoint()) {
				d.logEvent(eventlog.Debug, "android.u2.ready", eventlog.Duration("ms", time.Since(started)))
				return nil
			}
			// A running shell whose endpoint does not answer is torn down and
			// restarted; the pinned client behaves the same and never retries
			// individual calls.
			d.stopServerLocked(ctx, 2*time.Second)
		} else {
			d.server = nil
		}
	}

	// Any old server (for example the Python service's) would keep serving old
	// code even after the JAR is replaced, so it is stopped before the push.
	if err := d.stopRemote(ctx, 3*time.Second); err != nil {
		return err
	}

	remoteSum := d.remoteHash(ctx)
	if remoteSum != expectedArtifactDigest() {
		if err := d.pushJar(ctx); err != nil {
			return err
		}
	}

	exe, err := d.exePath()
	if err != nil {
		return err
	}
	process, err := d.startServerProcess(ctx, exe, d.timeout)
	if err != nil {
		return err
	}
	d.server = process
	readyDeadline := 30 * time.Second
	if d.timeout > readyDeadline {
		readyDeadline = d.timeout
	}
	if err := d.waitReady(ctx, process, readyDeadline); err != nil {
		process.stop(2 * time.Second)
		d.server = nil
		_ = d.stopRemote(ctx, 2*time.Second)
		d.logEvent(eventlog.Warn, "android.u2.start_failed",
			eventlog.Str("code", CodeOf(err)),
			eventlog.Duration("ms", time.Since(started)))
		return err
	}
	d.logEvent(eventlog.Info, "android.u2.started",
		eventlog.Int("count", 1),
		eventlog.Duration("ms", time.Since(started)))
	return nil
}

// pushJar pushes the verified artifact to the fixed remote path.
func (d *Device) pushJar(ctx context.Context) error {
	exe, err := d.exePath()
	if err != nil {
		return err
	}
	result, err := d.run(ctx, 60*time.Second, exe,
		d.adbArgs("push", d.localJarPath, d.remoteJarPath)...)
	if err != nil {
		return newErr(CodeArtifact, "the u2 artifact could not be pushed", nil)
	}
	if result.ExitCode != 0 {
		return newErr(CodeArtifact, "the u2 artifact could not be pushed", nil)
	}
	if d.remoteHash(ctx) != expectedArtifactDigest() {
		return newErr(CodeArtifact, "the pushed u2 artifact did not verify", nil)
	}
	d.logEvent(eventlog.Info, "android.u2.pushed", eventlog.Int("count", 1))
	return nil
}

// stopRemote kills only this project's known u2 app_process pattern.
func (d *Device) stopRemote(ctx context.Context, timeout time.Duration) error {
	command := "pkill -f '" + u2ProcessPattern + "'"
	result, err := d.shellWith(ctx, timeout, command)
	if err != nil {
		return err
	}
	// pkill exits 1 when no process matched, which is the expected idle case.
	if result.ExitCode != 0 && result.ExitCode != 1 {
		return exitDetail(result.ExitCode)
	}
	return nil
}

// StopU2 stops only this project's known u2 app_process pattern and waits
// (bounded) until the RPC port stops answering.
func (d *Device) StopU2(ctx context.Context) error {
	if d == nil {
		return errNilDevice
	}
	d.mu.Lock()
	stopped := d.stopServerLocked(ctx, 3*time.Second)
	remoteErr := d.stopRemote(ctx, 3*time.Second)
	d.mu.Unlock()
	if stopped {
		d.logEvent(eventlog.Info, "android.u2.stopped", eventlog.Int("count", 1))
	}
	if remoteErr != nil {
		return remoteErr
	}
	return nil
}

// stopServerLocked releases the local shell and waits (bounded) for the
// endpoint to stop answering. The caller holds mu.
func (d *Device) stopServerLocked(ctx context.Context, killAfter time.Duration) bool {
	if d.server == nil {
		return false
	}
	d.server.stop(killAfter)
	d.server = nil
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !d.ping(ctx, d.client, d.endpoint()) {
			return true
		}
		select {
		case <-ctx.Done():
			return true
		case <-time.After(200 * time.Millisecond):
		}
	}
	return true
}
