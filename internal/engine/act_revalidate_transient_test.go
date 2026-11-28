package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestActRevalidateTransientSingleRetryAndStop(t *testing.T) {
	const targetLabel = "Continue"
	const stopDelay = 10 * time.Millisecond
	bounds := device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}
	capture := func(display device.Display, label string) device.Capture {
		return revalCapture(display, revalRoot(display.ID, 1),
			revalButton(1, 0, 1, display.ID, bounds, label, "", "continue_btn"))
	}
	for _, tc := range []struct {
		name, first, second   string
		code, reason, deliver string
		captures, taps        int
		wait                  time.Duration
	}{
		{name: "first_frame_succeeds", first: targetLabel, captures: 1, taps: 1},
		{name: "second_frame_succeeds", first: "Loading", second: targetLabel, captures: 2, taps: 1, wait: 150 * time.Millisecond},
		{name: "second_frame_delivered", first: "Loading", second: "Try again", captures: 2,
			code: protocol.CodeTargetChanged, reason: targetChangedReasonTargetMissing, deliver: "Try again", wait: 150 * time.Millisecond},
		{name: "second_capture_unavailable", first: "Loading", captures: 2,
			code: protocol.CodeHierarchyFailed, wait: 150 * time.Millisecond},
		{name: "capability_change_has_no_retry", first: targetLabel, captures: 1,
			code: protocol.CodeTargetChanged, reason: targetChangedReasonCapabilityChanged, deliver: targetLabel},
		{name: "stop_interrupts_wait", first: "Loading", captures: 1, code: protocol.CodeStopped, wait: stopDelay},
		{name: "deadline_interrupts_wait", first: "Loading", captures: 1, code: protocol.CodeStopped, wait: stopDelay},
		{name: "foreground_changes_during_wait", first: "Loading", second: targetLabel, captures: 2,
			code: protocol.CodeForegroundChanged, deliver: targetLabel, wait: 150 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var logged bytes.Buffer
				var logger *eventlog.Logger
				if tc.code == "" {
					logger = eventlog.New(&logged, "instance-test")
					defer logger.Close()
				}
				h := newHarnessWithLogger(t, false, logger)
				rec := &revalDevice{fakeDevice: h.device}
				h.engine.dev = rec
				h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
					return capture(display, targetLabel), nil
				})
				start := h.startSession()
				button := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, targetLabel)
				captures := 0
				lastCaptureAt := time.Time{}
				var foregroundChanged atomic.Bool
				rec.onCapture = func(_ context.Context, display device.Display) (device.Capture, error, bool) {
					if len(rec.recordedTaps()) != 0 {
						return capture(display, targetLabel), nil, true // Post-dispatch observation.
					}
					captures++
					lastCaptureAt = time.Now()
					if captures == 2 && tc.code == protocol.CodeHierarchyFailed {
						return device.Capture{}, errors.New("unreadable second frame"), true
					}
					if captures > 1 {
						fresh := capture(display, tc.second)
						if foregroundChanged.Load() {
							fresh.Foreground = revalForeground("com.other", 77)
						}
						return fresh, nil, true
					}
					fresh := capture(display, tc.first)
					if tc.reason == targetChangedReasonCapabilityChanged {
						fresh.Nodes[1].Enabled = false
					}
					switch tc.name {
					case "deadline_interrupts_wait":
						h.engine.mu.Lock()
						h.engine.work.Deadline = time.Now().Add(stopDelay)
						h.engine.mu.Unlock()
						time.AfterFunc(stopDelay, func() { h.engine.housekeeping(context.Background()) })
					case "foreground_changes_during_wait":
						time.AfterFunc(stopDelay, func() { foregroundChanged.Store(true) })
					case "stop_interrupts_wait":
						time.AfterFunc(stopDelay, func() {
							if _, stopErr := h.engine.Stop(context.Background(), &protocol.ControlStopRequest{
								AppInstanceID: h.appInstance, StopSeq: 1, Reason: "user_stop",
							}); stopErr != nil {
								t.Errorf("STOP failed: %v", stopErr)
							}
						})
					}
					return fresh, nil, true
				}

				began := time.Now()
				request := h.tapRefRequest(t, start.Observation.ID, button.Ref)
				result := h.actWith(t, "Ptransient", request)
				if captures != tc.captures || len(rec.recordedTaps()) != tc.taps {
					t.Fatalf("revalidation captures=%d taps=%d, want %d and %d, result=%+v",
						captures, len(rec.recordedTaps()), tc.captures, tc.taps, result)
				}
				elapsed := lastCaptureAt.Sub(began)
				if tc.taps == 0 {
					elapsed = time.Since(began)
				}
				if elapsed != tc.wait {
					t.Fatalf("revalidation elapsed=%v, want %v", elapsed, tc.wait)
				}
				if tc.code == "" {
					proposalRequireExecuted(t, result)
					logger.Close()
					attempt := "attempt=1"
					if tc.captures == 2 {
						attempt = "attempt=2"
					}
					var targetEvents, recoveryEvents []string
					for _, line := range strings.Split(logged.String(), "\n") {
						if strings.Contains(line, " act.auto_recovery ") {
							recoveryEvents = append(recoveryEvents, line)
						}
						if strings.Contains(line, " act.target ") && strings.Contains(line, `operation_id="`+result.OperationID+`"`) {
							targetEvents = append(targetEvents, line)
						}
					}
					if len(targetEvents) != 1 || !strings.Contains(targetEvents[0], attempt) {
						t.Fatalf("act.target events = %v, want one with %s", targetEvents, attempt)
					}
					if tc.captures == 2 {
						want := protocol.AutoRecoveryView{Strategy: protocol.AutoRecoveryTransientRetry,
							OriginalRef: button.Ref, RecoveredRef: button.Ref, Attempts: 2}
						if result.AutoRecovery == nil || *result.AutoRecovery != want {
							t.Fatalf("auto_recovery = %+v, want %+v", result.AutoRecovery, want)
						}
						if len(recoveryEvents) != 1 {
							t.Fatalf("want exactly one recovery event: %v", recoveryEvents)
						}
						event := recoveryEvents[0]
						for _, field := range []string{`strategy="transient_retry"`, `attempt=2`, `outcome="proven"`,
							`reason="target_missing"`, `ms=150`, `operation_id="Ptransient"`,
							`observation_id="` + start.Observation.ID + `"`} {
							if !strings.Contains(event, field) {
								t.Fatalf("recovery event missing %s: %q", field, event)
							}
						}
						if strings.Contains(event, "ref=") || strings.Contains(event, targetLabel) || strings.Contains(event, `"`+button.Ref+`"`) {
							t.Fatalf("recovery event leaked target details: %q", event)
						}
						requireRecoveryReplay(t, h, request, result)
					} else if len(recoveryEvents) != 0 || result.AutoRecovery != nil {
						t.Fatalf("first attempt invented recovery: events=%v, result=%+v", recoveryEvents, result.AutoRecovery)
					}
					return
				}
				requireNotExecuted(t, result, tc.code)
				if tc.name == "deadline_interrupts_wait" && result.Phase != "work_deadline" {
					t.Fatalf("deadline did not replay the frozen phase: %+v", result)
				}
				if result.AutoRecovery != nil {
					t.Fatalf("rejected attempt saved recovery: %+v", result.AutoRecovery)
				}
				switch tc.reason {
				case targetChangedReasonTargetMissing:
					requireActRejectionDetails(t, result, "tap", button.Ref, tc.reason)
					requireRecoveryAttempts(t, result, protocol.AutoRecoveryTransientRetry, protocol.AutoRecoveryContainerRebind)
				case targetChangedReasonCapabilityChanged:
					requireActRejectionDetails(t, result, "tap", button.Ref, tc.reason)
					requireRecoveryAttempts(t, result)
				default:
					for _, key := range []string{"phase", "kind", "recovery_attempted", "original_ref"} {
						if _, exists := result.Error.Details[key]; exists {
							t.Fatalf("%s failure unexpectedly includes %s: %v", tc.code, key, result.Error.Details)
						}
					}
				}
				if tc.deliver == "" {
					if result.Observation != nil {
						t.Fatal("rejection delivered an observation")
					}
					return
				}
				if result.Observation == nil || result.Observation.ID == start.Observation.ID ||
					result.Observation.ID != h.currentObservationID() {
					t.Fatalf("rejection did not deliver a new current observation: %+v", result.Observation)
				}
				mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, tc.deliver)
			})
		})
	}
}

func TestActRevalidateTier5OcclusionAndColocatedSync(t *testing.T) {
	t.Run("sync_shift", func(t *testing.T) {
		rect := func(centerY, height int) device.Rect {
			return device.Rect{Left: 100, Top: centerY - height/2, Right: 300, Bottom: centerY + height/2}
		}
		for _, tc := range []struct {
			name                       string
			childShift, colocatedShift int
			heights                    [4]int
			want                       bool
		}{
			{"move_together", 100, 100, [4]int{40, 40, 40, 40}, true},
			{"minimum_inclusive", 60, 44, [4]int{40, 40, 40, 40}, true},
			{"minimum_exceeded", 60, 43, [4]int{40, 40, 40, 40}, false},
			{"quarter_height_inclusive", -80, -60, [4]int{40, 40, 40, 80}, true},
			{"quarter_height_exceeded", -81, -60, [4]int{40, 40, 40, 80}, false},
			{"empty_old_child", 60, 60, [4]int{0, 40, 40, 40}, false},
			{"empty_fresh_child", 60, 60, [4]int{40, 0, 40, 40}, false},
			{"empty_old_colocated", 60, 60, [4]int{40, 40, 0, 40}, false},
			{"empty_fresh_colocated", 60, 60, [4]int{40, 40, 40, 0}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := colocatedSyncShiftOK(rect(200, tc.heights[0]), rect(200+tc.childShift, tc.heights[1]),
					rect(210, tc.heights[2]), rect(210+tc.colocatedShift, tc.heights[3]))
				if got != tc.want {
					t.Fatalf("colocatedSyncShiftOK = %v, want %v", got, tc.want)
				}
			})
		}
	})

	for _, tc := range []struct {
		name       string
		overlay    bool
		tier       int
		wantReason string
	}{
		{name: "tier_1_overlay", overlay: true, tier: 1},
		{name: "tier_2_overlay", overlay: true, tier: 2},
		{name: "tier_3_overlay", overlay: true, tier: 3},
		{name: "tier_4_overlay", overlay: true, tier: 4, wantReason: targetChangedReasonScopeBlocked},
		{name: "tier_5_overlay", overlay: true, tier: 5, wantReason: targetChangedReasonScopeBlocked},
		{name: "tier_5_clear", tier: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			display := targetProofDisplay()
			bounds := device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}
			fresh := revalCapture(display, revalRoot(display.ID, 1),
				revalButton(1, 0, 1, display.ID, bounds, "Continue", "", "continue_btn"))
			if tc.overlay {
				fresh.Nodes[0].Children = append(fresh.Nodes[0].Children, 2)
				fresh.Nodes = append(fresh.Nodes,
					revalButton(2, 0, 1, display.ID, bounds, "Overlay", "", "overlay_btn"))
			}
			projector := &testProjector{builder: observation.NewBuilder()}
			projection, err := projector.Preview(&fresh)
			if err != nil {
				t.Fatal(err)
			}
			view, apiErr := buildTapPublicView(projection.Tree, &fresh, projection.Source)
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			topology, apiErr := validateTapRawTopology(&fresh)
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			if check := checkTapSourceMapping(view, topology, 1); check != tapSourceValid {
				t.Fatalf("target source mapping = %v, want valid", check)
			}
			target := view.byRaw[1]
			input := &tapInput{view: view, capture: &fresh, topology: topology,
				target: target, targetRaw: &fresh.Nodes[1], targetIndex: 1}
			scope, apiErr := collectTapScope(input)
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			x, y := bounds.Center()
			plan := &tapPlan{action: "tap", x: x, y: y}
			match := actMatch{tier: tc.tier, raw: input.targetRaw}
			frame, apiErr := (&Engine{}).resolveDirectTapScopeFrame(context.Background(), nil,
				nil, &fresh, topology, view, plan, match, target, 1, input, scope, tapFrameOptions{})
			if tc.wantReason != "" {
				if frame != nil || apiErr == nil || apiErr.Code != protocol.CodeTargetChanged ||
					apiErr.Details["reason"] != tc.wantReason {
					t.Fatalf("frame=%+v error=%+v, want target_changed/%s", frame, apiErr, tc.wantReason)
				}
			} else if apiErr != nil || frame == nil || frame.x != x || frame.y != y {
				t.Fatalf("frame=%+v error=%+v, want original point", frame, apiErr)
			}
		})
	}
}
