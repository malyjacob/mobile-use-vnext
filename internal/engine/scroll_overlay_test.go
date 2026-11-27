package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func scrollOverlayTestRect(left, top, right, bottom int) device.Rect {
	return device.Rect{Left: left, Top: top, Right: right, Bottom: bottom}
}

func scrollOverlayTestList(parent int, bounds device.Rect) rawNodeSpec {
	return rawNodeSpec{Parent: parent, Bounds: bounds, Class: "android.widget.ScrollView", Scrollable: true}
}

func scrollOverlayTestButton(parent int, bounds device.Rect, label string) rawNodeSpec {
	return rawNodeSpec{Parent: parent, Bounds: bounds, Class: "android.widget.Button", Text: label, Clickable: true}
}

func scrollOverlayRefTarget(t *testing.T, h *harness, role protocol.Role, bounds device.Rect) *protocol.Target {
	t.Helper()
	node := tapPublicNode(currentTree(t, h.engine).Root, role, bounds)
	if node == nil {
		t.Fatalf("missing published %s at %+v", role, bounds)
	}
	return &protocol.Target{UseRef: true, Ref: node.Ref}
}

func scrollOverlayReject(t *testing.T, h *harness, observationID string, target *protocol.Target,
	direction, amount, code, reason string) map[string]any {
	t.Helper()
	captures := h.device.captureCount()
	result := h.actP(t, "", observationID, protocol.Operation{
		Type: "scroll", Container: target, Direction: direction, Amount: amount,
	})
	details := proposalRequireStaticReason(t, result, code, reason)
	if h.device.captureCount() != captures || observationStatus(h, observationID) != ObservationReady {
		t.Fatal("static scroll rejection captured or consumed the observation")
	}
	proposalRequireNoDispatch(t, h)
	return details
}

func scrollOverlayRequireNoCandidates(t *testing.T, details map[string]any) {
	t.Helper()
	if candidates, exists := details["candidates"]; exists {
		t.Fatalf("candidates = %#v, want candidates omitted", candidates)
	}
}

func TestScrollOverlayRectUnionArea(t *testing.T) {
	rects := []device.Rect{
		scrollOverlayTestRect(-2, -2, 4, 4),
		scrollOverlayTestRect(0, 1, 6, 5),
		scrollOverlayTestRect(-2, -2, 4, 4), // duplicate
		scrollOverlayTestRect(6, 1, 8, 5),   // adjacent
		scrollOverlayTestRect(0, 0, 0, 8),   // empty
	}
	if area := rectUnionArea64(rects); area != 56 {
		t.Fatalf("union area = %d, want 56", area)
	}
}

func TestScrollStaticCandidatesCrossWindowIsolationOrderIndependent(t *testing.T) {
	appList := scrollOverlayTestRect(0, 100, 1080, 2200)
	dialogList := scrollOverlayTestRect(100, 500, 980, 1800)
	dialogButton := scrollOverlayTestRect(200, 40, 880, 100)
	for _, order := range []struct {
		name        string
		dialog, app int
	}{
		{"dialog_first", 1, 2},
		{"app_first", 2, 1},
	} {
		t.Run(order.name, func(t *testing.T) {
			for _, mode := range []string{"ref_not_scrollable", "ref_no_safe_trajectory", "point_with_scrim", "folded_windows_no_scrim"} {
				t.Run(mode, func(t *testing.T) {
					specs := []rawNodeSpec{
						{Parent: -1, Bounds: screenRect()},
						{Parent: 0, Bounds: screenRect(), Class: "android.widget.FrameLayout", Hidden: true},
						{Parent: 0, Bounds: screenRect(), Class: "android.widget.FrameLayout", Hidden: true},
						scrollOverlayTestList(order.app, appList),
						scrollOverlayTestButton(order.dialog, dialogButton, "Dismiss"),
					}
					switch mode {
					case "ref_no_safe_trajectory":
						specs = append(specs, scrollOverlayTestList(order.dialog, dialogList))
					case "point_with_scrim":
						specs = append(specs, rawNodeSpec{Parent: order.dialog, Bounds: screenRect(),
							Class: "android.view.View", Clickable: true})
					case "folded_windows_no_scrim":
						specs = append(specs, scrollOverlayTestButton(order.dialog, screenRect(), "Dialog surface"))
					}
					h := newHarness(t)
					h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
						capture := buildCapture(display, specs)
						capture.Nodes[capture.Root].Package = ""
						if mode == "folded_windows_no_scrim" {
							// Drop these raw wrappers during projection while preserving their children.
							capture.Nodes[order.app].DisplayID = display.ID + 1
							capture.Nodes[order.dialog].DisplayID = display.ID + 1
						}
						return capture, nil
					})
					start := h.startSession()
					appRef := scrollOverlayRefTarget(t, h, protocol.RoleScroll, appList).Ref
					target := scrollOverlayRefTarget(t, h, protocol.RoleButton, dialogButton)
					code, reason := protocol.CodeNodeUnsupported, staticReasonNotScrollable
					switch mode {
					case "ref_no_safe_trajectory":
						target = scrollOverlayRefTarget(t, h, protocol.RoleScroll, dialogList)
						code, reason = protocol.CodeNoSafeTrajectory, staticReasonTrajectoryBlocked
					case "point_with_scrim", "folded_windows_no_scrim":
						target = &protocol.Target{X: 540, Y: 70}
						code, reason = protocol.CodeInvalidTarget, staticReasonNoScrollContainer
					}
					details := scrollOverlayReject(t, h, start.Observation.ID, target,
						protocol.ScrollDirectionDown, protocol.ScrollAmountHalf, code, reason)
					if mode == "folded_windows_no_scrim" {
						proposalRequireCandidateExecutable(t, proposalCandidates(t, details), appRef)
						return
					}
					scrollOverlayRequireNoCandidates(t, details)
				})
			}
		})
	}
}

func TestScrollOverlayOccludedCandidatesExcluded(t *testing.T) {
	drawerRect := scrollOverlayTestRect(0, 0, 864, 2400)
	drawerList := scrollOverlayTestRect(0, 273, 864, 2214)
	drawerHeader := scrollOverlayTestRect(0, 0, 864, 273)
	drawerFooter := scrollOverlayTestRect(0, 2214, 864, 2400)
	drawerRow := scrollOverlayTestRect(0, 300, 864, 420)
	drawerSpecs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "androidx.drawerlayout.widget.DrawerLayout"},
		{Parent: 0, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		scrollOverlayTestList(1, scrollOverlayTestRect(0, 0, 1080, 2248)),
		scrollOverlayTestList(2, scrollOverlayTestRect(0, 5, 1080, 2248)),
		scrollOverlayTestList(3, scrollOverlayTestRect(0, 300, 1080, 1000)),
		scrollOverlayTestList(3, scrollOverlayTestRect(0, 1200, 1080, 1800)),
		{Parent: 0, Bounds: drawerRect, Class: "android.widget.FrameLayout"},
		scrollOverlayTestButton(6, drawerHeader, "Drawer header"),
		scrollOverlayTestList(6, drawerList),
		scrollOverlayTestButton(8, drawerRow, "Drawer entry"),
		scrollOverlayTestButton(6, drawerFooter, "Drawer footer"),
	}
	for _, tc := range []struct {
		name                    string
		role                    protocol.Role
		bounds                  device.Rect
		direction, code, reason string
	}{
		{"drawer_list", protocol.RoleScroll, drawerList, protocol.ScrollDirectionDown,
			protocol.CodeNoSafeTrajectory, staticReasonTrajectoryBlocked},
		{"drawer_header", protocol.RoleButton, drawerHeader, protocol.ScrollDirectionDown,
			protocol.CodeNodeUnsupported, staticReasonNotScrollable},
		{"drawer_row_horizontal", protocol.RoleButton, drawerRow, protocol.ScrollDirectionLeft,
			protocol.CodeNodeUnsupported, staticReasonAxisMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.device.scriptCaptures(scrollPageStep(drawerSpecs))
			start := h.startSession()
			target := scrollOverlayRefTarget(t, h, tc.role, tc.bounds)
			details := scrollOverlayReject(t, h, start.Observation.ID, target,
				tc.direction, protocol.ScrollAmountHalf, tc.code, tc.reason)
			if tc.name == "drawer_header" {
				candidates := proposalCandidates(t, details)
				listRef := scrollOverlayRefTarget(t, h, protocol.RoleScroll, drawerList).Ref
				if len(candidates) != 1 || candidates[0]["ref"] != listRef {
					t.Fatalf("drawer header candidates = %#v, want only front list %s", candidates, listRef)
				}
				return
			}
			scrollOverlayRequireNoCandidates(t, details)
		})
	}

	t.Run("drawer_point_without_scroll_hit", func(t *testing.T) {
		h := newHarness(t)
		h.device.scriptCaptures(scrollPageStep(drawerSpecs))
		start := h.startSession()
		details := scrollOverlayReject(t, h, start.Observation.ID, &protocol.Target{X: 540, Y: 2300},
			protocol.ScrollDirectionDown, protocol.ScrollAmountHalf, protocol.CodeInvalidTarget, staticReasonNoScrollContainer)
		listRef := scrollOverlayRefTarget(t, h, protocol.RoleScroll, drawerList).Ref
		proposalRequireCandidateRefs(t, proposalCandidates(t, details), listRef)
	})

	t.Run("drawer_foreground_first", func(t *testing.T) {
		near := scrollOverlayTestRect(0, 2250, 864, 2400)
		far := scrollOverlayTestRect(900, 0, 1080, 2400)
		specs := append(append([]rawNodeSpec(nil), drawerSpecs...),
			scrollOverlayTestList(6, near), scrollOverlayTestList(0, far))
		h := newHarness(t)
		h.device.scriptCaptures(scrollPageStep(specs))
		start := h.startSession()
		target := scrollOverlayRefTarget(t, h, protocol.RoleButton, drawerHeader)
		details := scrollOverlayReject(t, h, start.Observation.ID, target, protocol.ScrollDirectionDown,
			protocol.ScrollAmountPage, protocol.CodeNodeUnsupported, staticReasonNotScrollable)
		candidates := proposalCandidates(t, details)
		nearRef := scrollOverlayRefTarget(t, h, protocol.RoleScroll, near).Ref
		farRef := scrollOverlayRefTarget(t, h, protocol.RoleScroll, far).Ref
		if first := candidates[0]["ref"]; first != nearRef {
			t.Fatalf("first candidate = %v, want same-drawer scroll %s (candidates %v)",
				first, nearRef, proposalCandidateRefs(candidates))
		}
		proposalRequireCandidateExecutable(t, candidates, nearRef)
		proposalRequireCandidateExecutable(t, candidates, farRef)
	})

	t.Run("preceding_scrim", func(t *testing.T) {
		button := scrollOverlayTestRect(0, 0, 1080, 150)
		h := newHarness(t)
		h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
			capture := buildCapture(display, []rawNodeSpec{
				{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
				{Parent: 0, Bounds: screenRect(), Class: "android.view.View", Clickable: true},
				scrollOverlayTestList(0, screenRect()),
				scrollOverlayTestList(2, scrollOverlayTestRect(0, 200, 1080, 2200)),
				scrollOverlayTestButton(0, button, "Close editor"),
			})
			capture.Nodes[1].ResourceID, capture.Nodes[1].ResourceSet = "com.example:id/v_mask", true
			return capture, nil
		})
		start := h.startSession()
		target := scrollOverlayRefTarget(t, h, protocol.RoleButton, button)
		details := scrollOverlayReject(t, h, start.Observation.ID, target, protocol.ScrollDirectionDown,
			protocol.ScrollAmountPage, protocol.CodeNodeUnsupported, staticReasonNotScrollable)
		scrollOverlayRequireNoCandidates(t, details)
	})

	t.Run("hollow_hud", func(t *testing.T) {
		list := scrollOverlayTestRect(0, 140, 1080, 2260)
		top := scrollOverlayTestRect(0, 0, 1080, 140)
		bottom := scrollOverlayTestRect(0, 2260, 1080, 2400)
		h := newHarness(t)
		h.device.scriptCaptures(scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			scrollOverlayTestList(0, list),
			{Parent: 0, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			scrollOverlayTestButton(2, top, "Top bar"),
			scrollOverlayTestButton(2, bottom, "Bottom bar"),
			scrollOverlayTestButton(2, scrollOverlayTestRect(860, 2050, 1040, 2230), "Compose"),
		}))
		start := h.startSession()
		listRef := scrollOverlayRefTarget(t, h, protocol.RoleScroll, list).Ref
		t.Run("top_ref", func(t *testing.T) {
			target := scrollOverlayRefTarget(t, h, protocol.RoleButton, top)
			details := scrollOverlayReject(t, h, start.Observation.ID, target, protocol.ScrollDirectionDown,
				protocol.ScrollAmountPage, protocol.CodeNodeUnsupported, staticReasonNotScrollable)
			proposalRequireCandidateExecutable(t, proposalCandidates(t, details), listRef)
		})
		t.Run("bottom_point", func(t *testing.T) {
			details := scrollOverlayReject(t, h, start.Observation.ID, &protocol.Target{X: 540, Y: 2300},
				protocol.ScrollDirectionDown, protocol.ScrollAmountPage, protocol.CodeInvalidTarget, staticReasonNoScrollContainer)
			proposalRequireCandidateExecutable(t, proposalCandidates(t, details), listRef)
		})
	})

	t.Run("preceding_video_player", func(t *testing.T) {
		comments := scrollOverlayTestRect(0, 700, 1080, 2400)
		controls := scrollOverlayTestRect(0, 0, 1080, 140)
		h := newHarness(t)
		h.device.scriptCaptures(scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			{Parent: 0, Bounds: screenRect(), Class: "android.widget.FrameLayout", Clickable: true},
			scrollOverlayTestButton(1, controls, "Pause video"),
			scrollOverlayTestList(0, comments),
		}))
		start := h.startSession()
		target := scrollOverlayRefTarget(t, h, protocol.RoleButton, controls)
		details := scrollOverlayReject(t, h, start.Observation.ID, target, protocol.ScrollDirectionDown,
			protocol.ScrollAmountPage, protocol.CodeNodeUnsupported, staticReasonNotScrollable)
		commentsRef := scrollOverlayRefTarget(t, h, protocol.RoleScroll, comments).Ref
		proposalRequireCandidateExecutable(t, proposalCandidates(t, details), commentsRef)
	})

	t.Run("small_overlay_blocks_last_safe_span", func(t *testing.T) {
		bar := scrollOverlayTestRect(0, 0, 1080, 220)
		blocker := scrollOverlayTestRect(0, 220, 1080, 2400)
		h := newHarness(t)
		h.device.scriptCaptures(scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			scrollOverlayTestList(0, blocker),
			scrollOverlayTestList(0, screenRect()),
			scrollOverlayTestButton(0, bar, "Small overlay"),
		}))
		start := h.startSession()
		target := scrollOverlayRefTarget(t, h, protocol.RoleButton, bar)
		details := scrollOverlayReject(t, h, start.Observation.ID, target, protocol.ScrollDirectionDown,
			protocol.ScrollAmountHalf, protocol.CodeNodeUnsupported, staticReasonNotScrollable)
		scrollOverlayRequireNoCandidates(t, details)
	})

	t.Run("mini_player", func(t *testing.T) {
		bar := scrollOverlayTestRect(0, 2256, 1080, 2400)
		h := newHarness(t)
		h.device.scriptCaptures(scrollPageStep([]rawNodeSpec{
			{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
			scrollOverlayTestList(0, screenRect()),
			scrollOverlayTestButton(0, bar, "Mini player"),
		}))
		start := h.startSession()
		target := scrollOverlayRefTarget(t, h, protocol.RoleButton, bar)
		details := scrollOverlayReject(t, h, start.Observation.ID, target, protocol.ScrollDirectionDown,
			protocol.ScrollAmountPage, protocol.CodeNodeUnsupported, staticReasonNotScrollable)
		listRef := scrollOverlayRefTarget(t, h, protocol.RoleScroll, screenRect()).Ref
		proposalRequireCandidateExecutable(t, proposalCandidates(t, details), listRef)
	})
}
