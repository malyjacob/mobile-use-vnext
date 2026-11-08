package android

import (
	"context"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

const testRunID = "0f0e0d0c-0b0a-4908-8706-050403020100"

func TestShellRunDirValidation(t *testing.T) {
	if _, err := shellRunDir(testRunID); err != nil {
		t.Fatalf("valid id rejected: %v", err)
	}
	for _, bad := range []string{
		"", "../x", "R" + strings.Repeat("a", 32),
		"0F0E0D0C-0B0A-4908-8706-050403020100",
		"0f0e0d0c-0b0a-4908-8706-05040302010g",
	} {
		if _, err := shellRunDir(bad); err == nil {
			t.Fatalf("invalid id accepted: %q", bad)
		}
	}
}

func TestParseShellStart(t *testing.T) {
	start, ok := parseShellStart("start_v1\t123\t123\t456\tboot")
	if !ok || !start.HasIdentity || start.PID != 123 || start.PGID != 123 ||
		start.StartTicks != 456 || start.BootID != "boot" {
		t.Fatalf("start=%+v ok=%v", start, ok)
	}
	for _, bad := range []string{
		"", "start_v1\t0\t0\t0\tboot", "start_v1\tx\t1\t1\tboot",
		"start_v1\t1\t1\tx\tboot", "start_v2\t1\t1\t1\tboot",
	} {
		if _, ok := parseShellStart(bad); ok {
			t.Fatalf("malformed start accepted: %q", bad)
		}
	}
}

func TestParseShellDoneTriState(t *testing.T) {
	done, ok := parseShellDone("done_v1\texited\t7\t69632\t1")
	if !ok || done.Reason != "exited" || done.ExitCode == nil || *done.ExitCode != 7 ||
		!done.HasBytes || done.Bytes != 69632 || done.Truncated == nil || !*done.Truncated {
		t.Fatalf("done=%+v ok=%v", done, ok)
	}
	done, ok = parseShellDone("done_v1\ttimeout\t-\t-\t-")
	if !ok || done.ExitCode != nil || done.HasBytes || done.Truncated != nil {
		t.Fatalf("unknown fields must stay nil: %+v", done)
	}
	done, ok = parseShellDone("done_v1\texited\t0\t0\t0")
	if !ok || done.ExitCode == nil || *done.ExitCode != 0 || done.Truncated == nil || *done.Truncated {
		t.Fatalf("zero/empty must be explicit: %+v", done)
	}
}

func TestParseShellSnapshot(t *testing.T) {
	snapshot, err := parseShellSnapshot("version:1\n" +
		"start:start_v1\t123\t123\t456\tboot\n" +
		"done:done_v1\texited\t0\t3\t0\n" +
		"pid:123\nalive:1\n")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.HasStart || !snapshot.HasDone || !snapshot.Alive ||
		snapshot.Done.ExitCode == nil || *snapshot.Done.ExitCode != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if _, err := parseShellSnapshot("version:2\n"); err == nil {
		t.Fatal("unsupported runner version accepted")
	}
}

func TestPrepareAndStartManagedShell(t *testing.T) {
	d := newStubDevice(t, "shell")
	ctx := context.Background()
	command := []byte("id\n")
	if err := d.PrepareManagedShell(ctx, testRunID, command); err != nil {
		t.Fatalf("PrepareManagedShell: %v", err)
	}
	start, err := d.StartManagedShell(ctx, testRunID, false, 30)
	if err != nil {
		t.Fatalf("StartManagedShell: %v", err)
	}
	if !start.HasIdentity || start.PID != 123 || start.StartTicks != 456 {
		t.Fatalf("start=%+v", start)
	}
	snapshot, err := d.InspectManagedShell(ctx, testRunID)
	if err != nil {
		t.Fatalf("InspectManagedShell: %v", err)
	}
	if !snapshot.HasStart || !snapshot.Alive {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	output, err := d.ReadManagedShellOutput(ctx, testRunID)
	if err != nil {
		t.Fatalf("ReadManagedShellOutput: %v", err)
	}
	if string(output) != "hi\n" {
		t.Fatalf("output=%q", output)
	}
}

func TestRootAvailable(t *testing.T) {
	d := newStubDevice(t, "shell")
	available, err := d.RootAvailable(context.Background())
	if err != nil {
		t.Fatalf("RootAvailable: %v", err)
	}
	if !available {
		t.Fatal("root should be reported available")
	}
}

func TestShellSnapshotIdentityFields(t *testing.T) {
	var snapshot device.ShellSnapshot
	if snapshot.HasStart {
		t.Fatal("zero snapshot must not claim a start")
	}
}
