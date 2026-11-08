package android

import (
	"context"
	_ "embed"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// The managed shell runner is a fixed script plus one UUID-named directory per
// run. The service writes the user command as stdin bytes and verifies the
// device copy by length; the command text never appears in an argv or a log.

const (
	// ShellRunnerVersion is the device runner metadata protocol.
	ShellRunnerVersion = "1"
	// ShellOutputLimit is the retained output prefix. The device file is one
	// additional 4 KiB block so truncation can be proven.
	ShellOutputLimit = 65536
	// ShellStartTimeout bounds the wait for the runner's start metadata.
	ShellStartTimeout = 3 * time.Second
	// ShellTermWait is the bounded wait after TERM before KILL.
	ShellTermWait = 2 * time.Second
	// ShellKillWait is the bounded wait after KILL.
	ShellKillWait = 3 * time.Second

	defaultShellRunnerPath = "/data/local/tmp/mobile-use-shell-runner.sh"
	defaultShellRoot       = "/data/local/tmp/mobile-use-shell"
)

// ErrShellNotStarted means the runner directory exists but the command was
// provably never started.
var ErrShellNotStarted = errors.New("managed shell never started")

// ErrShellStartUnconfirmed means the dispatch was issued but no start or finish
// evidence was readable within the bounded window.
var ErrShellStartUnconfirmed = errors.New("managed shell start is unconfirmed")

// runnerScript is the fixed device-side script embedded at build time.
//
//go:embed shellrunner.sh
var runnerScript string

// shellRunDir validates one service-generated runner id and returns its fixed
// device directory. Names are never derived from user input.
func shellRunDir(runID string) (string, error) {
	if len(runID) != 36 {
		return "", newErr(CodeInvalidArgs, "invalid shell run id", nil)
	}
	for i := 0; i < len(runID); i++ {
		c := runID[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return "", newErr(CodeInvalidArgs, "invalid shell run id", nil)
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return "", newErr(CodeInvalidArgs, "invalid shell run id", nil)
			}
		}
	}
	return defaultShellRoot + "/" + runID, nil
}

// runShellScript executes one management script through the device shell. The
// script is a service constant and contains no user text. Input bytes, when
// present, are stdin to that script.
func (d *Device) runShellScript(ctx context.Context, timeout time.Duration, script string, input []byte) (ShellResult, error) {
	if d == nil {
		return ShellResult{}, errNilDevice
	}
	exe, err := d.exePath()
	if err != nil {
		return ShellResult{}, err
	}
	return d.runInput(ctx, timeout, input, exe,
		d.adbArgs("shell", "sh -c "+shellQuote(script))...)
}

// EnsureShellRunner verifies or installs the fixed runner script. The device
// copy must match the embedded bytes; a stale copy is replaced, never trusted.
func (d *Device) EnsureShellRunner(ctx context.Context) error {
	if d == nil {
		return errNilDevice
	}
	want := sha256Hex([]byte(runnerScript))
	if d.remoteHashOf(ctx, defaultShellRunnerPath) == want {
		return nil
	}
	script := "umask 022; mkdir -p " + shellQuote(defaultShellRoot) +
		"; chmod 755 " + shellQuote(defaultShellRoot) +
		"; cat > " + shellQuote(defaultShellRunnerPath) +
		"; chmod 755 " + shellQuote(defaultShellRunnerPath)
	if result, err := d.runShellScript(ctx, 15*time.Second, script, []byte(runnerScript)); err != nil {
		return err
	} else if result.ExitCode != 0 {
		return newErr(CodeArtifact, "the shell runner could not be written", nil)
	}
	if d.remoteHashOf(ctx, defaultShellRunnerPath) != want {
		return newErr(CodeArtifact, "the shell runner did not verify", nil)
	}
	d.logEvent(eventlog.Debug, "android.shell.runner_pushed", eventlog.Int("count", 1))
	return nil
}

// PrepareManagedShell creates the run directory and writes the exact command
// bytes. The caller must persist its ownership before calling this.
func (d *Device) PrepareManagedShell(ctx context.Context, runID string, command []byte) error {
	if d == nil {
		return errNilDevice
	}
	if len(command) == 0 {
		return newErr(CodeInvalidArgs, "empty shell command", nil)
	}
	dir, err := shellRunDir(runID)
	if err != nil {
		return err
	}
	if err := d.EnsureShellRunner(ctx); err != nil {
		return err
	}
	script := "umask 022; mkdir -p " + shellQuote(dir) +
		"; chmod 755 " + shellQuote(dir) +
		"; rm -f " + shellQuote(dir+"/command") + " " + shellQuote(dir+"/start") +
		" " + shellQuote(dir+"/done") + " " + shellQuote(dir+"/exit") +
		" " + shellQuote(dir+"/cmdpid") + " " + shellQuote(dir+"/stop") +
		" " + shellQuote(dir+"/reason") + " " + shellQuote(dir+"/output") +
		"; umask 077; cat > " + shellQuote(dir+"/command") +
		"; chmod 600 " + shellQuote(dir+"/command")
	result, err := d.runShellScript(ctx, 15*time.Second, script, command)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return newErr(CodeArtifact, "the shell command could not be written", nil)
	}
	check, err := d.runShellScript(ctx, 8*time.Second,
		"wc -c < "+shellQuote(dir+"/command"), nil)
	if err != nil {
		return err
	}
	got, convErr := strconv.Atoi(strings.TrimSpace(string(check.Stdout)))
	if convErr != nil || got != len(command) {
		return newErr(CodeArtifact, "the device command copy did not verify", nil)
	}
	return nil
}

// StartManagedShell detaches the runner and waits for bounded start evidence.
// It returns ErrShellNotStarted when the runner provably never ran the command
// and ErrShellStartUnconfirmed when no conclusion is possible.
func (d *Device) StartManagedShell(ctx context.Context, runID string, root bool, deadline time.Duration) (device.ShellStart, error) {
	if d == nil {
		return device.ShellStart{}, errNilDevice
	}
	dir, err := shellRunDir(runID)
	if err != nil {
		return device.ShellStart{}, err
	}
	seconds := int(deadline / time.Second)
	if seconds <= 0 {
		seconds = 30
	}
	inner := "setsid /system/bin/sh " + shellQuote(defaultShellRunnerPath) + " " +
		shellQuote(dir) + " " + strconv.Itoa(seconds) +
		" </dev/null >/dev/null 2>&1 &"
	script := inner
	if root {
		script = "su -c " + shellQuote(inner)
	}
	result, err := d.runShellScript(ctx, 10*time.Second, script, nil)
	if err != nil {
		return device.ShellStart{}, err
	}
	if result.ExitCode != 0 {
		if root {
			return device.ShellStart{}, newErr(CodeRootDenied, "root shell could not start", nil)
		}
		return device.ShellStart{}, newErr(CodeAvailability, "managed shell could not start", nil)
	}
	deadlineAt := time.Now().Add(ShellStartTimeout)
	for {
		snapshot, inspectErr := d.InspectManagedShell(ctx, runID)
		if inspectErr != nil {
			return device.ShellStart{}, inspectErr
		}
		if snapshot.HasStart {
			return snapshot.Start, nil
		}
		if snapshot.HasDone {
			return device.ShellStart{}, ErrShellNotStarted
		}
		if time.Now().After(deadlineAt) {
			return device.ShellStart{}, ErrShellStartUnconfirmed
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return device.ShellStart{}, ctx.Err()
		}
	}
}

// InspectManagedShell reads bounded runner metadata. It never reads the
// output prefix; callers do that once after the run is final.
func (d *Device) InspectManagedShell(ctx context.Context, runID string) (device.ShellSnapshot, error) {
	if d == nil {
		return device.ShellSnapshot{}, errNilDevice
	}
	dir, err := shellRunDir(runID)
	if err != nil {
		return device.ShellSnapshot{}, err
	}
	script := "d=" + shellQuote(dir) + "\n" +
		"printf 'version:'; cat \"$d/version\" 2>/dev/null; printf '\\n'\n" +
		"printf 'start:'; cat \"$d/start\" 2>/dev/null; printf '\\n'\n" +
		"printf 'done:'; cat \"$d/done\" 2>/dev/null; printf '\\n'\n" +
		"printf 'pid:'; cat \"$d/cmdpid\" 2>/dev/null; printf '\\n'\n" +
		"p=$(cat \"$d/cmdpid\" 2>/dev/null)\n" +
		"printf 'alive:'; if [ -n \"$p\" ] && [ -d \"/proc/$p\" ]; then echo 1; else echo 0; fi\n"
	result, err := d.runShellScript(ctx, 8*time.Second, script, nil)
	if err != nil {
		return device.ShellSnapshot{}, err
	}
	if result.ExitCode != 0 {
		return device.ShellSnapshot{}, newErr(CodeAvailability, "shell metadata could not be read", nil)
	}
	return parseShellSnapshot(string(result.Stdout))
}

// ReadManagedShellOutput reads the bounded output prefix. The device file is
// capped at one block over the limit, so a truncation fact is provable.
func (d *Device) ReadManagedShellOutput(ctx context.Context, runID string) ([]byte, error) {
	if d == nil {
		return nil, errNilDevice
	}
	dir, err := shellRunDir(runID)
	if err != nil {
		return nil, err
	}
	exe, exeErr := d.exePath()
	if exeErr != nil {
		return nil, exeErr
	}
	result, runErr := d.run(ctx, 10*time.Second, exe,
		d.adbArgs("exec-out", "dd bs=4096 count=17 if="+shellQuote(dir+"/output")+" 2>/dev/null")...)
	if runErr != nil {
		return nil, runErr
	}
	if result.ExitCode != 0 {
		return nil, newErr(CodeAvailability, "shell output could not be read", nil)
	}
	return result.Stdout, nil
}

// TerminateManagedShell stops the exact recorded process group and confirms
// its disappearance. It never signals a bare PID or a package name.
func (d *Device) TerminateManagedShell(ctx context.Context, runID string, root bool, reason string) (device.ShellTermination, error) {
	if d == nil {
		return device.ShellTermination{Unknown: true}, errNilDevice
	}
	dir, err := shellRunDir(runID)
	if err != nil {
		return device.ShellTermination{Unknown: true}, err
	}
	snapshot, inspectErr := d.InspectManagedShell(ctx, runID)
	if inspectErr != nil {
		return device.ShellTermination{Unknown: true}, inspectErr
	}
	if !snapshot.HasStart {
		if snapshot.HasDone {
			return device.ShellTermination{Confirmed: true, AlreadyGone: true}, nil
		}
		return device.ShellTermination{Unknown: true}, nil
	}
	start := snapshot.Start
	if !shellIdentityAlive(ctx, d, start) {
		result := device.ShellTermination{Confirmed: true, AlreadyGone: true, IdentityMatch: true}
		if snapshot.HasDone && snapshot.Done.ExitCode != nil {
			result.ExitCode = snapshot.Done.ExitCode
		}
		return result, nil
	}
	reason = shellReason(reason)
	writeScript := "printf '%s\\n' " + shellQuote(reason) + " > " + shellQuote(dir+"/reason") +
		"; : > " + shellQuote(dir+"/stop")
	if _, err := d.runShellScript(ctx, 8*time.Second, writeScript, nil); err != nil {
		return device.ShellTermination{Unknown: true}, err
	}
	killScript := "kill -TERM -" + strconv.Itoa(start.PGID) + " 2>/dev/null"
	runKill := func(script string) error {
		if root {
			script = "su -c " + shellQuote(script)
		}
		result, runErr := d.runShellScript(ctx, 8*time.Second, script, nil)
		if runErr != nil {
			return runErr
		}
		if result.ExitCode != 0 {
			return newErr(CodeAvailability, "shell termination signal failed", nil)
		}
		return nil
	}
	if err := runKill(killScript); err != nil {
		return device.ShellTermination{Unknown: true, IdentityMatch: true}, err
	}
	if d.waitShellGone(ctx, start, ShellTermWait) {
		return d.finishTermination(ctx, runID, start), nil
	}
	if err := runKill("kill -KILL -" + strconv.Itoa(start.PGID) + " 2>/dev/null"); err != nil {
		return device.ShellTermination{Unknown: true, IdentityMatch: true}, err
	}
	if d.waitShellGone(ctx, start, ShellKillWait) {
		return d.finishTermination(ctx, runID, start), nil
	}
	return device.ShellTermination{Unknown: true, IdentityMatch: true, Signal: "KILL"}, nil
}

func (d *Device) finishTermination(ctx context.Context, runID string, start device.ShellStart) device.ShellTermination {
	result := device.ShellTermination{Confirmed: true, IdentityMatch: true, Signal: "TERM"}
	snapshot, err := d.InspectManagedShell(ctx, runID)
	if err == nil && snapshot.HasDone && snapshot.Done.ExitCode != nil {
		result.ExitCode = snapshot.Done.ExitCode
	}
	return result
}

func (d *Device) waitShellGone(ctx context.Context, start device.ShellStart, timeout time.Duration) bool {
	deadlineAt := time.Now().Add(timeout)
	for {
		if !shellIdentityAlive(ctx, d, start) {
			return true
		}
		if time.Now().After(deadlineAt) || ctx.Err() != nil {
			return false
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false
		}
	}
}

// shellIdentityAlive checks PID + start ticks. A changed boot id or a missing
// /proc entry means the recorded process cannot be the original one.
func shellIdentityAlive(ctx context.Context, d *Device, start device.ShellStart) bool {
	if start.PID <= 0 {
		return false
	}
	script := "if [ -r /proc/" + strconv.Itoa(start.PID) + "/stat ]; then " +
		"awk '{print $22}' /proc/" + strconv.Itoa(start.PID) + "/stat; else echo gone; fi"
	result, err := d.runShellScript(ctx, 8*time.Second, script, nil)
	if err != nil || result.ExitCode != 0 {
		return false
	}
	text := strings.TrimSpace(string(result.Stdout))
	if text == "gone" || text == "" {
		return false
	}
	ticks, convErr := strconv.ParseUint(text, 10, 64)
	if convErr != nil {
		return false
	}
	return start.StartTicks == 0 || ticks == start.StartTicks
}

// RemoveManagedShell deletes one run directory. The name is re-validated.
func (d *Device) RemoveManagedShell(ctx context.Context, runID string, root bool) error {
	if d == nil {
		return errNilDevice
	}
	dir, err := shellRunDir(runID)
	if err != nil {
		return err
	}
	script := "rm -rf " + shellQuote(dir)
	if root {
		script = "su -c " + shellQuote(script)
	}
	result, err := d.runShellScript(ctx, 8*time.Second, script, nil)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return newErr(CodeAvailability, "shell run directory could not be removed", nil)
	}
	return nil
}

// RootAvailable probes the current device shell identity without changing it.
func (d *Device) RootAvailable(ctx context.Context) (bool, error) {
	if d == nil {
		return false, errNilDevice
	}
	result, err := d.runShellScript(ctx, 5*time.Second, "su -c 'id -u'", nil)
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		return false, nil
	}
	return strings.TrimSpace(string(result.Stdout)) == "0", nil
}

func shellReason(reason string) string {
	switch reason {
	case "timeout", "cancelled":
		return reason
	default:
		return "cancelled"
	}
}

// parseShellSnapshot parses the fixed metadata lines.
func parseShellSnapshot(text string) (device.ShellSnapshot, error) {
	var snapshot device.ShellSnapshot
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "version:"):
			if strings.TrimSpace(strings.TrimPrefix(line, "version:")) != ShellRunnerVersion {
				return snapshot, newErr(CodeArtifact, "unsupported shell runner version", nil)
			}
		case strings.HasPrefix(line, "start:"):
			value := strings.TrimSpace(strings.TrimPrefix(line, "start:"))
			if value == "" {
				continue
			}
			start, ok := parseShellStart(value)
			if !ok {
				return snapshot, newErr(CodeArtifact, "invalid shell start metadata", nil)
			}
			snapshot.HasStart, snapshot.Start = true, start
		case strings.HasPrefix(line, "done:"):
			value := strings.TrimSpace(strings.TrimPrefix(line, "done:"))
			if value == "" {
				continue
			}
			done, ok := parseShellDone(value)
			if !ok {
				return snapshot, newErr(CodeArtifact, "invalid shell done metadata", nil)
			}
			snapshot.HasDone, snapshot.Done = true, done
		case strings.HasPrefix(line, "alive:"):
			snapshot.Alive = strings.TrimSpace(strings.TrimPrefix(line, "alive:")) == "1"
		}
	}
	return snapshot, nil
}

func parseShellStart(value string) (device.ShellStart, bool) {
	parts := strings.Split(value, "\t")
	if len(parts) != 5 || parts[0] != "start_v1" {
		return device.ShellStart{}, false
	}
	pid, err1 := strconv.Atoi(parts[1])
	pgid, err2 := strconv.Atoi(parts[2])
	ticks, err3 := strconv.ParseUint(parts[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || pid <= 0 || pgid <= 0 {
		return device.ShellStart{}, false
	}
	return device.ShellStart{
		HasIdentity: true, PID: pid, PGID: pgid,
		StartTicks: ticks, BootID: parts[4],
	}, true
}

func parseShellDone(value string) (device.ShellDone, bool) {
	parts := strings.Split(value, "\t")
	if len(parts) != 5 || parts[0] != "done_v1" {
		return device.ShellDone{}, false
	}
	done := device.ShellDone{Reason: parts[1]}
	if parts[2] != "" && parts[2] != "-" {
		if code, err := strconv.Atoi(parts[2]); err == nil {
			done.ExitCode = &code
		}
	}
	if n, ok := shellBytes(parts[3]); ok {
		done.Bytes, done.HasBytes = n, true
	}
	if parts[4] == "0" || parts[4] == "1" {
		truncated := parts[4] == "1"
		done.Truncated = &truncated
	}
	return done, true
}

// shellBytes interprets the device byte counter. A missing or malformed count
// is unknown, never zero.
func shellBytes(value string) (int, bool) {
	if value == "" || value == "-" {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
