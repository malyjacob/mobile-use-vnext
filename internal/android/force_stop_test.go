package android

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// These tests run the real transport against the stubbed adb process, so the exact
// argv, the shell-v2 precondition and every receipt mapping are exercised without
// a device. A force-stop is never replayed: every case also counts invocations.

// forceStopArgv records one invocation and returns the recorded argv lines and
// the stub call count for a device built on the transcript.
func forceStopArgv(t *testing.T, transcript string) (*Device, string, string) {
	t.Helper()
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv.txt")
	countPath := filepath.Join(dir, "calls.txt")
	d := newStubDevice(t, transcript)
	d.testEnv = append(d.testEnv,
		"MOBILE_USE_FAKE_ADB_ARGV="+argvPath,
		fakeCallCountEnv+"="+countPath)
	return d, argvPath, countPath
}

// readInvocation returns the recorded argv of one stubbed invocation and the
// number of invocations the stub saw.
func readInvocation(t *testing.T, argvPath, countPath string) ([]string, int) {
	t.Helper()
	raw, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatalf("argv: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	count, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatalf("call count: %v", err)
	}
	return lines, len(count)
}

// TestForceStopPackageDispatchesExactCommand proves one call is one exact
// `am force-stop --user <id> <pkg>` dispatch, with no second invocation.
func TestForceStopPackageDispatchesExactCommand(t *testing.T) {
	cases := []struct {
		name string
		user int
		want string
	}{
		{"user zero", 0, "am force-stop --user 0 'com.example.app'"},
		{"user ten", 10, "am force-stop --user 10 'com.example.app'"},
		{"user with many digits", 999, "am force-stop --user 999 'com.example.app'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, argvPath, countPath := forceStopArgv(t, "force-stop")
			d.setShellV2(true)
			got, err := d.ForceStopPackage(t.Context(), "com.example.app", tc.user)
			if err != nil {
				t.Fatalf("ForceStopPackage: %v", err)
			}
			if got.Receipt != device.DispatchTerminated || !got.HasExitCode || got.ExitCode != 0 {
				t.Fatalf("result=%+v", got)
			}
			lines, calls := readInvocation(t, argvPath, countPath)
			want := []string{"-s", "127.0.0.1:5555", "shell", tc.want}
			if strings.Join(lines, "|") != strings.Join(want, "|") {
				t.Fatalf("argv=%q want %q", lines, want)
			}
			if calls != 1 {
				t.Fatalf("a force-stop must be dispatched exactly once, saw %d invocations", calls)
			}
		})
	}
}

// TestForceStopPackageNonZeroExitIsCompleted proves a device-side failure is a
// terminated command with its exit status, never an unknown receipt and never a
// second dispatch.
func TestForceStopPackageNonZeroExitIsCompleted(t *testing.T) {
	d, _, countPath := forceStopArgv(t, "force-stop-exit")
	d.setShellV2(true)
	got, err := d.ForceStopPackage(t.Context(), "com.example.app", 0)
	if err != nil {
		t.Fatalf("a completed non-zero exit is a fact, not a transport error: %v", err)
	}
	if got.Receipt != device.DispatchTerminated || !got.HasExitCode || got.ExitCode != 5 {
		t.Fatalf("result=%+v", got)
	}
	if count, readErr := os.ReadFile(countPath); readErr != nil || len(count) != 1 {
		t.Fatalf("calls=%q err=%v", count, readErr)
	}
}

// TestForceStopPackageTransportFailureIsUnknown proves a transport failure after
// start stays an unknown receipt: nothing at the command boundary proved the
// force-stop landed.
func TestForceStopPackageTransportFailureIsUnknown(t *testing.T) {
	d, _, _ := forceStopArgv(t, "force-stop-transport")
	d.setShellV2(true)
	got, err := d.ForceStopPackage(t.Context(), "com.example.app", 0)
	if got.Receipt != device.DispatchUnknown {
		t.Fatalf("receipt=%d", got.Receipt)
	}
	if CodeOf(err) != CodeTransport {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	if got.HasExitCode || got.ExitCode != 0 {
		t.Fatalf("an unknown receipt carries no exit status: %+v", got)
	}
}

// TestForceStopPackageRequiresShellV2 proves the composed command is never sent
// before the transport has proved it forwards remote exits.
func TestForceStopPackageRequiresShellV2(t *testing.T) {
	d, argvPath, _ := forceStopArgv(t, "force-stop")
	got, err := d.ForceStopPackage(t.Context(), "com.example.app", 0)
	if got.Receipt != device.DispatchNotDispatched {
		t.Fatalf("receipt=%d", got.Receipt)
	}
	if CodeOf(err) != CodeAvailability {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	if _, statErr := os.Stat(argvPath); statErr == nil {
		t.Fatal("a refused dispatch must not run a shell command")
	}
}

func TestForceStopPackageRejectsInvalidArguments(t *testing.T) {
	cases := []struct {
		name string
		pkg  string
		user int
	}{
		{"invalid package", "com.example app", 0},
		{"empty package", "", 0},
		{"negative user", "com.example.app", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, argvPath, _ := forceStopArgv(t, "force-stop")
			d.setShellV2(true)
			got, err := d.ForceStopPackage(t.Context(), tc.pkg, tc.user)
			if got.Receipt != device.DispatchNotDispatched || CodeOf(err) != CodeInvalidArgs {
				t.Fatalf("receipt=%d code=%q err=%v", got.Receipt, CodeOf(err), err)
			}
			if got.HasExitCode {
				t.Fatalf("a refused dispatch carries no exit status: %+v", got)
			}
			if _, statErr := os.Stat(argvPath); statErr == nil {
				t.Fatal("a refused dispatch must not run a shell command")
			}
		})
	}
}

func TestForceStopPackageNilDevice(t *testing.T) {
	var d *Device
	got, err := d.ForceStopPackage(t.Context(), "com.example.app", 0)
	if got.Receipt != device.DispatchNotDispatched || CodeOf(err) != CodeInvalidArgs {
		t.Fatalf("receipt=%d code=%q err=%v", got.Receipt, CodeOf(err), err)
	}
}

// TestForceStopPackageUserAppearsInArgvOnlyAsADecimal proves the user component
// of the command is a bare decimal integer, so a caller cannot smuggle shell text
// through it.
func TestForceStopPackageUserAppearsInArgvOnlyAsADecimal(t *testing.T) {
	d, argvPath, _ := forceStopArgv(t, "force-stop")
	d.setShellV2(true)
	if _, err := d.ForceStopPackage(t.Context(), "com.example.app", 10); err != nil {
		t.Fatalf("ForceStopPackage: %v", err)
	}
	raw, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatalf("argv: %v", err)
	}
	command := strings.TrimRight(string(raw), "\n")
	if !strings.HasSuffix(command, "am force-stop --user "+strconv.Itoa(10)+" 'com.example.app'") {
		t.Fatalf("argv=%q", command)
	}
}
