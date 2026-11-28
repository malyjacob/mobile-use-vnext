package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func staticScrollOperation(ref, direction string) protocol.Operation {
	return protocol.Operation{Type: "scroll", Container: &protocol.Target{UseRef: true, Ref: ref},
		Direction: direction, Amount: protocol.ScrollAmountPage}
}

func TestStaticScrollIssue52CoextensivePager(t *testing.T) {
	viewport := scrollOverlayTestRect(0, 200, 1080, 2200)
	page := scrollPageStep([]rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{Parent: 0, Bounds: viewport, Class: "androidx.viewpager.widget.ViewPager", Scrollable: true},
		{Parent: 1, Bounds: viewport, Class: "androidx.recyclerview.widget.RecyclerView", Scrollable: true},
		{Parent: 2, Bounds: scrollOverlayTestRect(0, 300, 1080, 500), Class: "android.widget.TextView", Text: "first"},
		{Parent: 2, Bounds: scrollOverlayTestRect(0, 600, 1080, 800), Class: "android.widget.TextView", Text: "second"},
	})
	for _, tc := range []struct {
		direction, mode string
		containerIndex  int
		gesture         swipeCall
	}{
		{protocol.ScrollDirectionDown, scrollModeInnerFallback, 2, swipeCall{displayID: 12, x1: 540, y1: 1680, x2: 540, y2: 720, durationMS: 400}},
		{protocol.ScrollDirectionLeft, scrollModeCoextensive, 1, swipeCall{displayID: 12, x1: 281, y1: 1200, x2: 799, y2: 1200, durationMS: 400}},
		{protocol.ScrollDirectionRight, scrollModeCoextensive, 1, swipeCall{displayID: 12, x1: 799, y1: 1200, x2: 281, y2: 1200, durationMS: 400}},
	} {
		t.Run(tc.direction, func(t *testing.T) {
			h, buffer, logger := issue42NewLoggedHarness(t)
			h.device.scriptCaptures(page)
			start := h.startSession()
			pager := issue42ScrollNode(t, h, viewport)
			list := findRole(pager.Children[0], protocol.RoleScroll)
			if pager.Axis != protocol.AxisX || list == nil || list.Axis != protocol.AxisY || rectFromBounds(list.Bounds) != viewport {
				t.Fatalf("fixture must publish coextensive x pager and y list: pager %+v, list %+v", pager, list)
			}
			operation := staticScrollOperation(pager.Ref, tc.direction)
			obs := observationStateByID(t, h.engine, start.Observation.ID)
			plan, err := resolveScrollPlan(obs, &operation)
			if err != nil || plan == nil || plan.containerIndex != tc.containerIndex || plan.mode != tc.mode {
				t.Errorf("plan = %+v, err = %+v, want index %d and mode %s", plan, err, tc.containerIndex, tc.mode)
			}
			h.device.scriptCaptures(page, page)
			result := h.actP(t, "", start.Observation.ID, operation)
			issue42RequireExecutedScroll(t, result)
			if result.AutoRecovery != nil {
				t.Fatalf("static scroll reported auto recovery: %+v", result.AutoRecovery)
			}
			proposalRequireSwipe(t, h, tc.gesture)
			issue42RequirePlan(t, issue42PlanFields(t, buffer, logger), tc.mode, tc.direction, protocol.ScrollAmountPage)
		})
	}
}

func TestStaticScrollIssue52RefusesMultipleOrOccludedLists(t *testing.T) {
	viewport := scrollOverlayTestRect(0, 200, 1080, 2200)
	outer := rawNodeSpec{Parent: 0, Bounds: viewport, Class: "androidx.viewpager.widget.ViewPager", Scrollable: true}
	scrim := rawNodeSpec{Parent: 0, Bounds: screenRect(), Class: "android.view.View", Clickable: true}
	root := rawNodeSpec{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"}
	for _, tc := range []struct {
		name string
		tree []rawNodeSpec
		mask bool
	}{
		{"two_coextensive_lists", []rawNodeSpec{root, outer, scrollOverlayTestList(1, viewport), scrollOverlayTestList(1, viewport)}, false},
		{"preceding_modal_scrim", []rawNodeSpec{root, scrim, outer, scrollOverlayTestList(2, viewport)}, true},
		{"cross_window_scrim", []rawNodeSpec{{Parent: -1, Bounds: screenRect()}, scrim, outer, scrollOverlayTestList(2, viewport)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.device.scriptCaptures(func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				capture := buildCapture(display, tc.tree)
				if tc.name == "cross_window_scrim" {
					capture.Nodes[capture.Root].Package = ""
				}
				return capture, nil
			})
			start := h.startSession()
			pager := issue42ScrollNode(t, h, viewport)
			if tc.mask {
				obs := observationStateByID(t, h.engine, start.Observation.ID)
				view, err := collectTapPublicView(obs)
				if err != nil {
					t.Fatal(err)
				}
				list := pager.Children[0]
				if !tryPlanScrollBase(obs, view, list, protocol.ScrollDirectionDown, protocol.ScrollAmountPage) {
					t.Fatal("fixture must allow the underlying list trajectory without the overlay filter")
				}
				ctx := newScrollOverlayContext(obs, view)
				if ctx.multiWindow != (tc.name == "cross_window_scrim") ||
					!isScrollCandidateOverlayOccluded(ctx, nil, list, protocol.ScrollDirectionDown, protocol.ScrollAmountPage) ||
					isScrollCandidateOverlayOccluded(ctx, pager, list, protocol.ScrollDirectionDown, protocol.ScrollAmountPage) {
					t.Fatal("fixture must distinguish strict modal masking from wanted-node exemption")
				}
			}
			details := issue42RequireStaticRejection(t, h, start.Observation.ID,
				staticScrollOperation(pager.Ref, protocol.ScrollDirectionDown), staticReasonAxisMismatch)
			proposalRequireDetailString(t, details, "scope_ref", pager.Ref)
		})
	}
}

func TestStaticScrollSameAxisFailureExcludesCoextensiveInner(t *testing.T) {
	outerRect := scrollOverlayTestRect(0, 200, 1080, 2200)
	innerRect := scrollOverlayTestRect(50, 200, 1080, 2200)
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		scrollOverlayTestList(0, outerRect),
		scrollOverlayTestList(1, innerRect),
	}
	// Each narrow sibling covers one of the outer scope's three axes. The
	// coextensive inner scope's shifted axes miss all three siblings.
	for _, x := range []int{270, 540, 810} {
		specs = append(specs, rawNodeSpec{Parent: 1, Bounds: scrollOverlayTestRect(x-3, 200, x+3, 2200),
			Class: "android.widget.HorizontalScrollView", Scrollable: true})
	}
	h := newHarness(t)
	h.device.scriptCaptures(scrollPageStep(specs))
	start := h.startSession()
	outer := issue42ScrollNode(t, h, outerRect)
	// The three side-by-side blockers infer x in projection. Pin y so
	// this contrast exercises failed same-axis planning rather than A1.
	outer.Axis = protocol.AxisY
	inner := issue42ScrollNode(t, h, innerRect)
	obs := observationStateByID(t, h.engine, start.Observation.ID)
	view, err := collectTapPublicView(obs)
	if err != nil {
		t.Fatal(err)
	}
	if !isCoextensiveRect(outerRect, innerRect) || !tryPlanScrollBase(obs, view, inner, protocol.ScrollDirectionDown, protocol.ScrollAmountPage) {
		t.Fatal("fixture must have one independently executable coextensive inner scope")
	}
	details := scrollOverlayReject(t, h, start.Observation.ID, &protocol.Target{UseRef: true, Ref: outer.Ref},
		protocol.ScrollDirectionDown, protocol.ScrollAmountPage, protocol.CodeNoSafeTrajectory, staticReasonTrajectoryBlocked)
	proposalRequireDetailString(t, details, "scope_ref", outer.Ref)
}

func TestScrollNonScrollableRecyclerProjectionRespectsDisabledBoundary(t *testing.T) {
	outerRect := scrollOverlayTestRect(0, 0, 1080, 1800)
	listRect := scrollOverlayTestRect(100, 900, 980, 1300)
	for _, tc := range []struct {
		name     string
		text     bool
		disabled bool
	}{
		{name: "recycler_ref"},
		{name: "child_text_ref", text: true},
		{name: "child_text_under_disabled_recycler", text: true, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const outerIndex, listIndex = 1, 2
			page := func(_ *fakeDevice, display device.Display) (device.Capture, error) {
				capture := buildCapture(display, []rawNodeSpec{
					{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
					{Parent: 0, Bounds: outerRect, Class: "androidx.core.widget.NestedScrollView", Scrollable: true},
					{Parent: outerIndex, Bounds: listRect, Class: "androidx.recyclerview.widget.RecyclerView", Disabled: tc.disabled},
					{Parent: listIndex, Bounds: scrollOverlayTestRect(100, 1000, 980, 1200), Class: "android.widget.TextView", Text: "inner-content"},
				})
				// Keep the anonymous wrapper visible to ref callers and model
				// the explicit scrollable=false attribute in the raw frame.
				list := &capture.Nodes[listIndex]
				list.HasScrollable = true
				list.ResourceID, list.ResourceSet = "com.example:id/inner_list", true
				return capture, nil
			}
			h, buffer, logger := issue42NewLoggedHarness(t)
			h.device.scriptCaptures(page)
			start := h.startSession()
			obs := observationStateByID(t, h.engine, start.Observation.ID)
			list := publicNodeByRawIndex(obs.Projection, listIndex)
			if list == nil || list.Role == protocol.RoleScroll || hasAction(list, protocol.ActionSwipe) ||
				(list.Enabled != nil && !*list.Enabled) != tc.disabled {
				t.Fatalf("projected RecyclerView = %+v, want a non-scroll node (disabled=%t)", list, tc.disabled)
			}
			target := list
			if tc.text {
				target = issue42TextNode(t, h, "inner-content")
				if !issue42IsDescendantOf(t, h, target, list) {
					t.Fatal("text is not a published descendant of the RecyclerView")
				}
			}
			operation := staticScrollOperation(target.Ref, protocol.ScrollDirectionDown)
			captures := h.device.captureCount()
			plan, err := resolveScrollPlan(obs, &operation)
			if err != nil || plan == nil || plan.containerIndex != outerIndex || plan.mode != scrollModeAncestorLift {
				t.Fatalf("plan = %+v, err = %v, want ancestor lift to NestedScrollView", plan, err)
			}
			if got := h.device.captureCount() - captures; got != 0 {
				t.Fatalf("static planner made %d extra captures", got)
			}
			h.device.scriptCaptures(page, page, page)
			result := h.actP(t, "", start.Observation.ID, operation)
			issue42RequireExecutedScroll(t, result)
			proposalRequireSwipe(t, h, swipeCall{displayID: 12, x1: 540, y1: 1332, x2: 540, y2: 468, durationMS: 400})
			requireQuality(t, result.Observation.Quality, protocol.QualityStable, 2, protocol.QualityStopStable)
			if got := h.device.captureCount() - captures; got != 3 {
				t.Fatalf("act made %d captures, want one revalidation and two post-settle samples", got)
			}
			issue42RequirePlan(t, issue42PlanFields(t, buffer, logger), scrollModeAncestorLift, operation.Direction, operation.Amount)
		})
	}
}

func TestStaticScrollWrapperFallback(t *testing.T) {
	outerRect := scrollOverlayTestRect(0, 200, 1080, 2200)
	wrapperRect := scrollOverlayTestRect(50, 200, 1080, 2200)
	half := scrollOverlayTestRect(50, 200, 1080, 1200)
	for _, tc := range []struct {
		name, ancestor, reason string
		inner                  device.Rect
		scrim, blocked         bool
	}{
		{name: "exactly_half", inner: half},
		{name: "axis_mismatch_ancestor", ancestor: "android.widget.HorizontalScrollView", inner: half},
		{name: "compatible_ancestor_wins", ancestor: "android.widget.ScrollView", inner: wrapperRect},
		{name: "below_half", inner: scrollOverlayTestRect(50, 200, 1080, 1199), reason: staticReasonNotScrollable},
		{name: "corner_strip", inner: scrollOverlayTestRect(50, 200, 160, 700), reason: staticReasonNotScrollable},
		{name: "masked", inner: half, scrim: true, reason: staticReasonNotScrollable},
		{name: "disabled_ancestor", inner: half, ancestor: "android.widget.ScrollView", reason: tapStaticReasonAncestorInvalid},
		{name: "geometryless_ancestor", inner: half, ancestor: "android.widget.ScrollView", reason: tapStaticReasonAncestorInvalid},
		{name: "compatible_ancestor_plan_fails", inner: wrapperRect, ancestor: "android.widget.ScrollView", blocked: true, reason: staticReasonTrajectoryBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			specs := []rawNodeSpec{{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"}}
			if tc.scrim {
				specs = append(specs, rawNodeSpec{Parent: 0, Bounds: screenRect(), Class: "android.view.View", Clickable: true})
			}
			parent := 0
			if tc.ancestor != "" {
				parent = len(specs)
				specs = append(specs, rawNodeSpec{Parent: 0, Bounds: outerRect, Class: tc.ancestor,
					Scrollable: true, Disabled: tc.name == "disabled_ancestor"})
			}
			wrapperIndex := len(specs)
			specs = append(specs, rawNodeSpec{Parent: parent, Bounds: wrapperRect, Class: "android.view.ViewGroup", Text: "page shell"},
				scrollOverlayTestList(wrapperIndex, tc.inner))
			if tc.blocked {
				for _, x := range []int{270, 540, 810} {
					specs = append(specs, rawNodeSpec{Parent: parent, Bounds: scrollOverlayTestRect(x-3, 200, x+3, 2200),
						Class: "android.widget.HorizontalScrollView", Scrollable: true})
				}
			}
			h := newHarness(t)
			page := scrollPageStep(specs)
			h.device.scriptCaptures(page)
			start := h.startSession()
			wrapper := proposalFindLabel(t, currentTree(t, h.engine).Root, "page shell")
			if wrapper.Role == protocol.RoleScroll {
				t.Fatal("wrapper was published as a scrolling node")
			}
			if tc.name == "geometryless_ancestor" {
				issue42ScrollNode(t, h, outerRect).Bounds = nil
			}
			if tc.blocked {
				// Keep the ancestor compatible even though its blockers lie side by side.
				issue42ScrollNode(t, h, outerRect).Axis = protocol.AxisY
			}
			operation := staticScrollOperation(wrapper.Ref, protocol.ScrollDirectionDown)
			obs := observationStateByID(t, h.engine, start.Observation.ID)
			view, viewErr := collectTapPublicView(obs)
			if viewErr != nil {
				t.Fatal(viewErr)
			}
			list := issue42ScrollNode(t, h, tc.inner)
			if !tryPlanScrollBase(obs, view, list, operation.Direction, operation.Amount) {
				t.Fatal("fixture must have an independently executable inner scope")
			}
			if tc.blocked {
				scrollOverlayReject(t, h, start.Observation.ID, operation.Container, operation.Direction, operation.Amount,
					protocol.CodeNoSafeTrajectory, tc.reason)
				return
			}
			if tc.reason != "" {
				issue42RequireStaticRejection(t, h, start.Observation.ID, operation, tc.reason)
				return
			}
			wantIndex, wantMode := wrapperIndex+1, scrollModeInnerFallback
			if tc.name == "compatible_ancestor_wins" {
				wantIndex, wantMode = parent, scrollModeAncestorLift
			}
			plan, err := resolveScrollPlan(obs, &operation)
			if err != nil || plan == nil || plan.containerIndex != wantIndex || plan.mode != wantMode {
				t.Errorf("plan = %+v, err = %+v, want index %d and mode %s", plan, err, wantIndex, wantMode)
			}
			h.device.scriptCaptures(page, page)
			issue42RequireExecutedScroll(t, h.actP(t, "", start.Observation.ID, operation))
			if swipeCount(h) != 1 {
				t.Fatalf("swipes = %d, want one", swipeCount(h))
			}
		})
	}
}
