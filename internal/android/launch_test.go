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
	removed, removeErr := d.RemoveTask(ctx, 42)
	if removeErr != nil || removed.Receipt != device.DispatchTerminated || removed.TaskID != 42 {
		t.Fatalf("RemoveTask: %+v err=%v", removed, removeErr)
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

func TestRemoveTaskReceipts(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		taskID     int
		receipt    device.DispatchReceipt
		code       string
	}{
		{"terminated", "launch", 42, device.DispatchTerminated, ""},
		{"device exit is unconfirmed", "task-remove-exit", 42, device.DispatchUnknown, CodeExitCode},
		{"transport is unknown", "task-remove-transport", 42, device.DispatchUnknown, CodeTransport},
		{"invalid task is not dispatched", "launch", 0, device.DispatchNotDispatched, CodeInvalidArgs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newStubDevice(t, tc.transcript)
			d.setShellV2(true)
			result, err := d.RemoveTask(t.Context(), tc.taskID)
			if result.Receipt != tc.receipt {
				t.Fatalf("receipt=%d want=%d", result.Receipt, tc.receipt)
			}
			if result.TaskID != tc.taskID {
				t.Fatalf("task id=%d want=%d", result.TaskID, tc.taskID)
			}
			if code := CodeOf(err); code != tc.code {
				t.Fatalf("code=%q want=%q err=%v", code, tc.code, err)
			}
		})
	}
}

func TestMoveTaskReceipts(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		rootID     int
		leafID     int
		target     int
		receipt    device.DispatchReceipt
		code       string
	}{
		{"terminated", "task-move", 42, 43, 61, device.DispatchTerminated, ""},
		{"device exit is unconfirmed", "task-move-exit", 42, 43, 61, device.DispatchUnknown, CodeExitCode},
		{"transport is unknown", "task-move-transport", 42, 43, 61, device.DispatchUnknown, CodeTransport},
		{"invalid root is not dispatched", "task-move", 0, 43, 61, device.DispatchNotDispatched, CodeInvalidArgs},
		{"invalid leaf is not dispatched", "task-move", 42, 0, 61, device.DispatchNotDispatched, CodeInvalidArgs},
		{"invalid display is not dispatched", "task-move", 42, 43, -1, device.DispatchNotDispatched, CodeInvalidArgs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newStubDevice(t, tc.transcript)
			d.setShellV2(true)
			result, err := d.MoveTask(t.Context(), tc.rootID, tc.leafID, tc.target)
			if result.Receipt != tc.receipt {
				t.Fatalf("receipt=%d want=%d", result.Receipt, tc.receipt)
			}
			if result.RootID != tc.rootID || result.LeafTaskID != tc.leafID ||
				result.TargetDisplayID != tc.target {
				t.Fatalf("ids=%+v", result)
			}
			if code := CodeOf(err); code != tc.code {
				t.Fatalf("code=%q want=%q err=%v", code, tc.code, err)
			}
		})
	}
}

func TestMoveTaskDoesNotClaimSourceDisplay(t *testing.T) {
	d := newStubDevice(t, "task-move")
	d.setShellV2(true)
	result, err := d.MoveTask(t.Context(), 42, 43, 61)
	if err != nil {
		t.Fatalf("MoveTask: %v", err)
	}
	// The dispatch boundary never names a source display, so the adapter must not
	// invent one (zero is the primary display, not "unknown").
	if result.SourceDisplayID != 0 {
		t.Fatalf("source display=%d", result.SourceDisplayID)
	}
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
