package android

import (
	"context"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

func TestResolveLauncher(t *testing.T) {
	d := newStubDevice(t, "launch")
	component, found, err := d.ResolveLauncher(context.Background(), "com.example.app")
	if err != nil || !found {
		t.Fatalf("ResolveLauncher: %v found=%v", err, found)
	}
	if component != "com.example.app/.MainActivity" {
		t.Fatalf("component=%q", component)
	}
}

func TestResolveLauncherRejectsInvalidPackage(t *testing.T) {
	d := newStubDevice(t, "launch")
	if _, _, err := d.ResolveLauncher(context.Background(), "com.example app"); err == nil {
		t.Fatal("expected a package rejection")
	}
}

func TestLaunchFocusAndRemoveDispatch(t *testing.T) {
	d := newStubDevice(t, "launch")
	d.setShellV2(true)
	ctx := context.Background()
	result, err := d.LaunchApp(ctx, 82, "com.example.app/.MainActivity")
	if err != nil || result.State != device.LaunchAccepted {
		t.Fatalf("LaunchApp: %v %+v", err, result)
	}
	if result.Receipt != device.DispatchTerminated {
		t.Fatalf("LaunchApp receipt=%d", result.Receipt)
	}
	receipt, err := d.FocusTask(ctx, 42)
	if err != nil || receipt != device.DispatchTerminated {
		t.Fatalf("FocusTask: receipt=%d err=%v", receipt, err)
	}
	if err := d.RemoveTask(ctx, 42); err != nil {
		t.Fatalf("RemoveTask: %v", err)
	}
	invalid, err := d.LaunchApp(ctx, 82, "not a component")
	if err == nil || invalid.Receipt != device.DispatchNotDispatched {
		t.Fatalf("component rejection=%+v err=%v", invalid, err)
	}
	receipt, err = d.FocusTask(ctx, 0)
	if err == nil || receipt != device.DispatchNotDispatched {
		t.Fatalf("task rejection receipt=%d err=%v", receipt, err)
	}
}

func TestLaunchAndFocusReceiptsSeparateDispatchFromResult(t *testing.T) {
	t.Run("launch semantic rejection", func(t *testing.T) {
		d := newStubDevice(t, "launch-rejected")
		d.setShellV2(true)
		result, err := d.LaunchApp(t.Context(), 82, "com.example.app/.MainActivity")
		if err != nil || result.State != device.LaunchRejected ||
			result.Receipt != device.DispatchTerminated {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("launch remote exit", func(t *testing.T) {
		d := newStubDevice(t, "launch-exit")
		d.setShellV2(true)
		result, err := d.LaunchApp(t.Context(), 82, "com.example.app/.MainActivity")
		if err != nil || result.State != device.LaunchRejected ||
			result.Receipt != device.DispatchTerminated || result.ExitCode != 5 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("launch missing status", func(t *testing.T) {
		d := newStubDevice(t, "launch-missing")
		d.setShellV2(true)
		result, err := d.LaunchApp(t.Context(), 82, "com.example.app/.MainActivity")
		if err != nil || result.State != device.LaunchUnknown ||
			result.Receipt != device.DispatchTerminated {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("launch transport", func(t *testing.T) {
		d := newStubDevice(t, "launch-transport")
		d.setShellV2(true)
		result, err := d.LaunchApp(t.Context(), 82, "com.example.app/.MainActivity")
		if CodeOf(err) != CodeTransport || result.State != device.LaunchUnknown ||
			result.Receipt != device.DispatchUnknown {
			t.Fatalf("result=%+v code=%q err=%v", result, CodeOf(err), err)
		}
	})

	t.Run("focus remote exit", func(t *testing.T) {
		d := newStubDevice(t, "focus-exit")
		d.setShellV2(true)
		receipt, err := d.FocusTask(t.Context(), 42)
		if receipt != device.DispatchTerminated || CodeOf(err) != CodeExitCode {
			t.Fatalf("receipt=%d code=%q err=%v", receipt, CodeOf(err), err)
		}
	})
}

func TestRecentTaskIDs(t *testing.T) {
	d := newStubDevice(t, "launch")
	ids, known := d.RecentTaskIDs(context.Background())
	if !known {
		t.Fatal("recents should be readable")
	}
	if !ids[42] || !ids[77] || len(ids) != 2 {
		t.Fatalf("ids=%v", ids)
	}
}

func TestRecentsUnreadableFormatDoesNotBecomeEmpty(t *testing.T) {
	for _, tc := range []struct{ text, state string }{
		{"unrecognized output", "format_unrecognized"},
		{"ACTIVITY MANAGER RECENT TASKS\n * Recent #0: Task{truncated", "format_unrecognized"},
		{"ACTIVITY MANAGER RECENT TASKS\n", "known_empty"},
		{" * Recent #0: Task{abc #41 type=standard}", "known_present"},
	} {
		if result := parseRecentTasks([]byte(tc.text)); result.State != tc.state {
			t.Fatalf("state=%s want=%s", result.State, tc.state)
		}
	}
}

func TestTasksOnDisplay(t *testing.T) {
	d := newStubDevice(t, "launch")
	tasks, known := d.TasksOnDisplay(context.Background(), 61)
	if !known {
		t.Fatal("tasks should be readable")
	}
	if len(tasks) != 1 || tasks[0].TaskID != 2120 || tasks[0].Package != "com.example.app" {
		t.Fatalf("tasks=%+v", tasks)
	}
}
