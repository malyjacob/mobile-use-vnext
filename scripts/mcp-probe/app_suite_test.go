package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// TestAppLifecycleClaimAgainstFakeAdapter exercises the controlled claim path:
// a shared controlled task exists on the primary display, a fresh observation
// rebuilds the ready O, and claim moves exactly that task into the session.
func TestAppLifecycleClaimAgainstFakeAdapter(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			adapter := newFakeAdapter()
			adapter.resultMode = mode
			adapter.primaryCandidate = true
			session := startFakeAdapter(t, adapter)
			serviceServer := startFakeStatusService(t, true)
			service := newTestServiceClient(t, serviceServer)

			var output bytes.Buffer
			reporter := newReporter(&output)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			probe := &probe{
				reporter: reporter, session: session, service: service,
				resultMode: mode, suite: suiteAppLifecycle,
				appTarget: controlledAppPackage,
			}
			if code := reporter.finish(probe.runChecks(ctx)); code != 0 {
				t.Fatalf("suite failed (exit %d):\n%s", code, output.String())
			}
			for _, want := range []string{
				"PASS app_open",
				"PASS app_list_controlled",
				"PASS app_kill task_id=42 target_pids=1 killed_pids=1 restarted=false",
				"PASS app_kill_removed task_id=42 remaining=0",
				"PASS app_close task_id=43 outcome=executed tasks=1 is_error=false",
				"PASS app_claim task_id=77 display_id=12 ownership=borrowed source_display_id=0",
				"RESULT PASS",
			} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q in:\n%s", want, output.String())
				}
			}
		})
	}
}

func TestAppLifecycleSuiteAgainstFakeAdapter(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			adapter := newFakeAdapter()
			adapter.resultMode = mode
			session := startFakeAdapter(t, adapter)
			serviceServer := startFakeStatusService(t, true)
			service := newTestServiceClient(t, serviceServer)

			var output bytes.Buffer
			reporter := newReporter(&output)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			probe := &probe{
				reporter: reporter, session: session, service: service,
				resultMode: mode, suite: suiteAppLifecycle,
				appTarget: controlledAppPackage,
			}
			if code := reporter.finish(probe.runChecks(ctx)); code != 0 {
				t.Fatalf("suite failed (exit %d):\n%s", code, output.String())
			}
			for _, want := range []string{
				"PASS app_open package=com.mobileuse.probe task_id=42 display_id=12 ownership=owned",
				"PASS app_list_controlled task_id=42 display_id=12 ownership=owned",
				"PASS app_kill task_id=42 target_pids=1 killed_pids=1 restarted=false",
				"PASS app_kill_removed task_id=42 remaining=0",
				"PASS app_close task_id=43 outcome=executed tasks=1 is_error=false",
				"SKIP app_claim reason=controlled_primary_task_absent",
				"PASS end",
				"RESULT PASS",
			} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q in:\n%s", want, output.String())
				}
			}
			if adapter.startCalls != 2 {
				t.Fatalf("start calls = %d, want 2", adapter.startCalls)
			}
		})
	}
}

// TestAppLifecycleRefusedKillAgainstFakeAdapter keeps the refusal branch: a
// still-valid kill refusal code must pass app_kill_refused, skip the removal
// proof and close the original controlled task.
func TestAppLifecycleRefusedKillAgainstFakeAdapter(t *testing.T) {
	for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			adapter := newFakeAdapter()
			adapter.resultMode = mode
			adapter.killRefusalCode = "kill_refused_task_elsewhere"
			session := startFakeAdapter(t, adapter)
			serviceServer := startFakeStatusService(t, true)
			service := newTestServiceClient(t, serviceServer)

			var output bytes.Buffer
			reporter := newReporter(&output)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			probe := &probe{
				reporter: reporter, session: session, service: service,
				resultMode: mode, suite: suiteAppLifecycle,
				appTarget: controlledAppPackage,
			}
			if code := reporter.finish(probe.runChecks(ctx)); code != 0 {
				t.Fatalf("suite failed (exit %d):\n%s", code, output.String())
			}
			for _, want := range []string{
				"PASS app_kill_refused task_id=42 code=kill_refused_task_elsewhere outcome=not_executed",
				"SKIP app_kill_removed reason=controlled_task_not_killed task_id=42",
				"PASS app_close task_id=42 outcome=executed tasks=1 is_error=false",
				"SKIP app_claim reason=controlled_primary_task_absent",
				"RESULT PASS",
			} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q in:\n%s", want, output.String())
				}
			}
		})
	}
}

// TestAppLifecycleKillRemovalFailureAgainstFakeAdapter is the reverse control
// for app_kill_removed: a still-listed killed task must fail the suite and stop
// the closed loop before it reopens or closes anything.
func TestAppLifecycleKillRemovalFailureAgainstFakeAdapter(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.keepKilledTask = true
	session := startFakeAdapter(t, adapter)
	serviceServer := startFakeStatusService(t, true)
	service := newTestServiceClient(t, serviceServer)

	var output bytes.Buffer
	reporter := newReporter(&output)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	probe := &probe{
		reporter: reporter, session: session, service: service,
		resultMode: resultModeBoth, suite: suiteAppLifecycle,
		appTarget: controlledAppPackage,
	}
	if code := reporter.finish(probe.runChecks(ctx)); code != 1 {
		t.Fatalf("exit = %d, want 1:\n%s", code, output.String())
	}
	for _, want := range []string{
		"FAIL app_kill_removed reason=killed_task_still_listed task_id=42 remaining=1",
		"RESULT FAIL",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "PASS app_close") {
		t.Fatalf("close ran after a failed removal proof:\n%s", output.String())
	}
}
