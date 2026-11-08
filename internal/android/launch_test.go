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
	ctx := context.Background()
	result, err := d.LaunchApp(ctx, 82, "com.example.app/.MainActivity")
	if err != nil || result.State != device.LaunchAccepted {
		t.Fatalf("LaunchApp: %v %+v", err, result)
	}
	if err := d.FocusTask(ctx, 42); err != nil {
		t.Fatalf("FocusTask: %v", err)
	}
	if err := d.RemoveTask(ctx, 42); err != nil {
		t.Fatalf("RemoveTask: %v", err)
	}
	if _, err := d.LaunchApp(ctx, 82, "not a component"); err == nil {
		t.Fatal("expected a component rejection")
	}
	if err := d.FocusTask(ctx, 0); err == nil {
		t.Fatal("expected a task id rejection")
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
