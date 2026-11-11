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
				"PASS app_kill_refused",
				"PASS app_close",
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
				"PASS app_kill_refused task_id=42 code=kill_refused_foreground",
				"PASS app_close task_id=42 outcome=executed tasks=1 is_error=false",
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
