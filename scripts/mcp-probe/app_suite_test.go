package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

const (
	expectedKillLine = "PASS app_kill pkg=com.mobileuse.probe user=0 src_task=42 " +
		"target_tasks=42 display=12 remaining_tasks=42 target_pids=101 killed_pids=101 " +
		"remaining_pids=empty restarted=false package_state=still_present"
	expectedPackageKillLine = "PASS app_kill_package pkg=com.mobileuse.probe user=0 " +
		"target_tasks=empty display=12 remaining_tasks=empty target_pids=101 " +
		"killed_pids=101 remaining_pids=empty restarted=false package_state=stopped_at_check"
)

// TestAppLifecycleClaimAgainstFakeAdapter exercises the controlled claim path:
// a shared controlled task exists on the primary display, a fresh observation
// rebuilds the ready O, and claim moves exactly that task into the session. The
// primary task also proves the package-entry ownership refusal.
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
				"PASS app_kill_refused task_id=42 code=kill_refused_task_elsewhere outcome=not_executed",
				"PASS app_close task_id=42 outcome=executed tasks=1 is_error=false",
				"PASS app_kill_primary_refused primary_task=77 code=kill_refused_task_elsewhere outcome=not_executed",
				"PASS app_close task_id=43 outcome=executed tasks=1 is_error=false",
				"PASS app_kill_package pkg=com.mobileuse.probe refused=kill_refused_task_elsewhere outcome=not_executed",
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
				expectedKillLine,
				"PASS app_close task_id=42 outcome=executed tasks=1 is_error=false",
				"SKIP app_kill_primary_refused reason=controlled_primary_task_absent",
				"PASS app_close task_id=43 outcome=executed tasks=1 is_error=false",
				expectedPackageKillLine,
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
// still-valid kill refusal code must pass app_kill_refused, keep the close path
// and also let the package entry stop before dispatch.
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
				"PASS app_close task_id=42 outcome=executed tasks=1 is_error=false",
				"SKIP app_kill_primary_refused reason=controlled_primary_task_absent",
				"PASS app_close task_id=43 outcome=executed tasks=1 is_error=false",
				"PASS app_kill_package pkg=com.mobileuse.probe refused=kill_refused_task_elsewhere outcome=not_executed",
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

// TestAppLifecycleUncertainKillAgainstFakeAdapter keeps an unconfirmed
// force-stop honest: the verified facts stay, restarted is an explicit null,
// the ready O is rebuilt and the cleanup close still runs.
func TestAppLifecycleUncertainKillAgainstFakeAdapter(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.killUncertain = true
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
	if code := reporter.finish(probe.runChecks(ctx)); code != 0 {
		t.Fatalf("suite failed (exit %d):\n%s", code, output.String())
	}
	for _, want := range []string{
		"PASS app_kill pkg=com.mobileuse.probe user=0 src_task=42 target_tasks=42 " +
			"display=12 remaining_tasks=unknown target_pids=101 killed_pids=unknown " +
			"remaining_pids=unknown restarted=null package_state=unknown " +
			"outcome=uncertain code=kill_unconfirmed",
		"PASS app_close task_id=42 outcome=executed tasks=1 is_error=false",
		"SKIP app_kill_primary_refused reason=controlled_primary_task_absent",
		"PASS app_kill_package pkg=com.mobileuse.probe user=0 target_tasks=empty " +
			"display=12 remaining_tasks=unknown target_pids=101 killed_pids=unknown " +
			"remaining_pids=unknown restarted=null package_state=unknown " +
			"outcome=uncertain code=kill_unconfirmed",
		"RESULT PASS",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, output.String())
		}
	}
}

// TestAppLifecycleKillFactFaultAgainstFakeAdapter is the reverse control for the
// new kill facts: a missing package_state or a stopped package that still lists
// a task must fail the suite before any cleanup runs.
func TestAppLifecycleKillFactFaultAgainstFakeAdapter(t *testing.T) {
	for _, fault := range []struct{ fault, reason string }{
		{"missing_package_state", "kill_package_state_invalid"},
		{"stopped_with_tasks", "kill_stopped_with_tasks"},
	} {
		fault := fault
		t.Run(fault.fault, func(t *testing.T) {
			adapter := newFakeAdapter()
			adapter.killFactFault = fault.fault
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
			if !strings.Contains(output.String(), "FAIL app_kill reason="+fault.reason) {
				t.Fatalf("missing kill failure %q in:\n%s", fault.reason, output.String())
			}
			if strings.Contains(output.String(), "PASS app_close") {
				t.Fatalf("cleanup ran after a failed kill fact:\n%s", output.String())
			}
		})
	}
}
