package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// These tests state the act target-resolution proposal as executable
// expectations. Error codes and reasons are written as literals so the file
// also compiles against the baseline before the new protocol constant lands.
const proposalNoSafeTrajectoryCode = "no_safe_trajectory"

var proposalLegacyDetailKeys = []string{
	"occluding_ref",
	"occluding_refs",
	"ambiguous_refs",
	"owner_refs",
	"nearest_actionable_ref",
	"safe_point",
	"safe_carrier_ref",
	"suggested_point",
	"suggested_carrier_ref",
}

func proposalCaptureStep(build func(device.Display) device.Capture) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return build(display), nil
	}
}

func proposalNode(index, parent, depth int, display device.Display,
	bounds device.Rect, class, text string) device.RawNode {
	return device.RawNode{
		Index: index, Parent: parent, Depth: depth, DisplayID: display.ID,
		Visible: true, RawBounds: bounds, Bounds: bounds,
		Package: "com.example", ClassName: class,
		Text: text, TextSet: true,
		DescSet: true, ResourceSet: true,
		HasEnabled: true, Enabled: true,
		HasClickable: true, Clickable: false,
		HasLongClick: true, LongClickable: false,
		HasScrollable: true, Scrollable: false,
		HasPassword: true, Password: false,
		HasCheckable: true, Checkable: false,
		HasFocused: true, Focused: false,
		HasSelected: true, Selected: false,
		HasChecked: true, Checked: false,
	}
}

func proposalScrollNode(index, parent, depth int, display device.Display,
	bounds device.Rect, class string) device.RawNode {
	node := proposalNode(index, parent, depth, display, bounds, class, "")
	node.HasScrollable = true
	node.Scrollable = true
	return node
}

func proposalClickableNode(index, parent, depth int, display device.Display,
	bounds device.Rect, class, text string) device.RawNode {
	node := proposalNode(index, parent, depth, display, bounds, class, text)
	node.HasClickable = true
	node.Clickable = true
	return node
}

func proposalRequireExecuted(t *testing.T, result *protocol.Result) {
	t.Helper()
	if result == nil || result.Outcome != OutcomeExecuted {
		t.Fatalf("result = %+v, want executed", result)
	}
	if result.Error != nil {
		t.Fatalf("executed result carries error %+v", result.Error)
	}
}

func proposalRequireStatic422(t *testing.T, result *protocol.Result, code string) map[string]any {
	t.Helper()
	requireNotExecuted(t, result, code)
	status := result.HTTPStatus
	if status == 0 {
		status = protocol.Status(result.Error.Code)
	}
	if status != 422 {
		t.Fatalf("HTTP status = %d, want 422 (%+v)", status, result.Error)
	}
	proposalRequireStatic422Contract(t, result)
	return result.Error.Details
}

func proposalRequireDetailString(t *testing.T, details map[string]any,
	key, want string) {
	t.Helper()
	if got := details[key]; got != want {
		t.Fatalf("details.%s = %#v, want %q (details %#v)", key, got, want, details)
	}
}

func proposalRequireStaticReason(t *testing.T, result *protocol.Result,
	code, reason string) map[string]any {
	t.Helper()
	details := proposalRequireStatic422(t, result, code)
	if got := details["reason"]; got != reason {
		t.Fatalf("reason = %#v, want %q (details %#v)", got, reason, details)
	}
	return details
}

func proposalRequireStatic422Contract(t *testing.T, result *protocol.Result) {
	t.Helper()
	if result == nil || result.Outcome != OutcomeNotExecuted || result.Error == nil {
		t.Fatalf("result = %+v, want not_executed with an error", result)
	}
	details := result.Error.Details
	if details == nil {
		t.Fatalf("%s rejection carries no details", result.Error.Code)
	}
	for _, key := range proposalLegacyDetailKeys {
		if value, exists := details[key]; exists {
			t.Fatalf("details still carry legacy key %q = %#v", key, value)
		}
	}
	reason, ok := details["reason"].(string)
	if !ok || reason == "" {
		t.Fatalf("details.reason = %#v, want a non-empty string", details["reason"])
	}
	if rawCandidates, exists := details["candidates"]; exists {
		candidates, ok := rawCandidates.([]map[string]any)
		if !ok {
			t.Fatalf("candidates = %#v (%T), want []map[string]any", rawCandidates, rawCandidates)
		}
		if len(candidates) == 0 || len(candidates) > 4 {
			t.Fatalf("candidate count = %d, want 1..4", len(candidates))
		}
		for index, candidate := range candidates {
			proposalValidateCandidate(t, index, candidate)
		}
	}
	if rawBlocking, exists := details["blocking_refs"]; exists {
		blockingRefs, ok := rawBlocking.([]string)
		if !ok {
			t.Fatalf("blocking_refs = %#v (%T), want []string", rawBlocking, rawBlocking)
		}
		if len(blockingRefs) == 0 || len(blockingRefs) > 3 {
			t.Fatalf("blocking_refs count = %d, want 1..3", len(blockingRefs))
		}
		for _, ref := range blockingRefs {
			if ref == "" {
				t.Fatalf("blocking_refs contains an empty ref: %#v", blockingRefs)
			}
		}
	}
	if result.Error.Code == protocol.CodeTextTargetUnknown {
		return
	}
	allowed := map[string]bool{
		"reason": true, "scope": true, "scope_ref": true,
		"target_actions": true, "ancestor_ref": true, "ancestor_actions": true,
		"candidates": true, "blocking_refs": true,
		// Contract 35 §4.2 adds exactly one objective geometry key to the
		// no_interactive_ancestor rejection; every other reason still omits it.
		"target_center": true,
	}
	for key := range details {
		if !allowed[key] {
			t.Fatalf("static act details carry undeclared key %q: %#v", key, details)
		}
	}
	scope, ok := details["scope"].(string)
	if !ok {
		t.Fatalf("details.scope = %#v, want target/ancestor/point", details["scope"])
	}
	switch scope {
	case "target", "ancestor":
		scopeRef, ok := details["scope_ref"].(string)
		if !ok || scopeRef == "" {
			t.Fatalf("details.scope_ref = %#v, want the checked ref", details["scope_ref"])
		}
		if scope == "ancestor" {
			ancestorRef, ok := details["ancestor_ref"].(string)
			if !ok || ancestorRef == "" {
				t.Fatalf("ancestor-scope details lack ancestor_ref: %#v", details)
			}
		}
	case "point":
		if _, exists := details["scope_ref"]; exists {
			t.Fatalf("point-scope details must omit scope_ref: %#v", details)
		}
	default:
		t.Fatalf("details.scope = %q, want target/ancestor/point", scope)
	}
	if result.Error.Code == protocol.CodeNodeUnsupported {
		rawActions, exists := details["target_actions"]
		if !exists {
			t.Fatalf("unsupported_node_action lacks target_actions: %#v", details)
		}
		actions, ok := rawActions.([]string)
		if !ok {
			t.Fatalf("target_actions = %#v (%T), want []string", rawActions, rawActions)
		}
		proposalRequireActionOrder(t, "target_actions", actions)
		if rawAncestor, exists := details["ancestor_actions"]; exists {
			ancestorActions, ok := rawAncestor.([]string)
			if !ok {
				t.Fatalf("ancestor_actions = %#v (%T), want []string", rawAncestor, rawAncestor)
			}
			proposalRequireActionOrder(t, "ancestor_actions", ancestorActions)
		}
	}
}

func proposalValidateCandidate(t *testing.T, index int, candidate map[string]any) {
	t.Helper()
	allowed := map[string]bool{
		"ref": true, "role": true, "actions": true,
		"bounds": true, "relation": true, "executable": true,
	}
	if len(candidate) != len(allowed) {
		t.Fatalf("candidate[%d] = %#v, want exactly six contract keys", index, candidate)
	}
	for key := range candidate {
		if !allowed[key] {
			t.Fatalf("candidate[%d] carries undeclared key %q: %#v", index, key, candidate)
		}
	}
	ref, ok := candidate["ref"].(string)
	if !ok || ref == "" {
		t.Fatalf("candidate[%d].ref = %#v, want a non-empty string", index, candidate["ref"])
	}
	role, ok := candidate["role"].(string)
	if !ok || role == "" {
		t.Fatalf("candidate[%d].role = %#v, want a non-empty string", index, candidate["role"])
	}
	rawActions, ok := candidate["actions"].([]string)
	if !ok {
		t.Fatalf("candidate[%d].actions = %#v (%T), want []string",
			index, candidate["actions"], candidate["actions"])
	}
	proposalRequireActionOrder(t, "candidate.actions", rawActions)
	bounds, ok := candidate["bounds"].([]int)
	if !ok || len(bounds) != 4 {
		t.Fatalf("candidate[%d].bounds = %#v (%T), want []int length 4",
			index, candidate["bounds"], candidate["bounds"])
	}
	if bounds[2] <= bounds[0] || bounds[3] <= bounds[1] {
		t.Fatalf("candidate[%d].bounds is not a positive rectangle: %#v", index, bounds)
	}
	relation, ok := candidate["relation"].(string)
	if !ok {
		t.Fatalf("candidate[%d].relation = %#v, want a string", index, candidate["relation"])
	}
	switch relation {
	case "child", "ancestor", "overlapping", "competing", "page_scroll":
	default:
		t.Fatalf("candidate[%d].relation = %q, want a closed enum value", index, relation)
	}
	if _, ok := candidate["executable"].(bool); !ok {
		t.Fatalf("candidate[%d].executable = %#v, want bool", index, candidate["executable"])
	}
}

func proposalRequireActionOrder(t *testing.T, field string, actions []string) {
	t.Helper()
	order := map[string]int{
		"tap": 0, "long_press": 1, "text": 2, "swipe": 3,
	}
	previous := -1
	seen := map[string]bool{}
	for _, action := range actions {
		position, ok := order[action]
		if !ok {
			t.Fatalf("%s contains unknown action %q: %#v", field, action, actions)
		}
		if seen[action] {
			t.Fatalf("%s contains duplicate action %q: %#v", field, action, actions)
		}
		if position <= previous {
			t.Fatalf("%s is not in tap/long_press/text/swipe order: %#v", field, actions)
		}
		seen[action] = true
		previous = position
	}
}

func proposalCandidates(t *testing.T, details map[string]any) []map[string]any {
	t.Helper()
	rawCandidates, exists := details["candidates"]
	if !exists {
		t.Fatalf("details lack candidates: %#v", details)
	}
	candidates, ok := rawCandidates.([]map[string]any)
	if !ok || len(candidates) == 0 {
		t.Fatalf("candidates = %#v (%T), want a non-empty []map[string]any",
			rawCandidates, rawCandidates)
	}
	return candidates
}

func proposalCandidateRefs(candidates []map[string]any) []string {
	refs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if ref, ok := candidate["ref"].(string); ok {
			refs = append(refs, ref)
		}
	}
	return refs
}

func proposalRequireCandidateRefs(t *testing.T, candidates []map[string]any, want ...string) {
	t.Helper()
	found := map[string]bool{}
	for _, ref := range proposalCandidateRefs(candidates) {
		found[ref] = true
	}
	for _, ref := range want {
		if !found[ref] {
			t.Fatalf("candidates %#v lack ref %s", proposalCandidateRefs(candidates), ref)
		}
	}
}

func proposalRequireCandidateRelation(t *testing.T, candidates []map[string]any,
	ref, relation string) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate["ref"] == ref {
			if got := candidate["relation"]; got != relation {
				t.Fatalf("candidate %s relation = %#v, want %q", ref, got, relation)
			}
			return
		}
	}
	t.Fatalf("candidates %#v lack ref %s", proposalCandidateRefs(candidates), ref)
}

func proposalRequireCandidateExecutable(t *testing.T, candidates []map[string]any, ref string) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate["ref"] != ref {
			continue
		}
		if executable, ok := candidate["executable"].(bool); !ok || !executable {
			t.Fatalf("candidate %s executable = %#v, want true", ref, candidate["executable"])
		}
		return
	}
	t.Fatalf("candidates %#v lack ref %s", proposalCandidateRefs(candidates), ref)
}

func proposalScrollNodesByBounds(root *protocol.Node, bounds device.Rect) []*protocol.Node {
	var scrollNodes []*protocol.Node
	var walk func(node *protocol.Node)
	walk = func(node *protocol.Node) {
		if node == nil {
			return
		}
		if node.Role == protocol.RoleScroll && node.Bounds != nil &&
			rectFromBounds(node.Bounds) == bounds {
			scrollNodes = append(scrollNodes, node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return scrollNodes
}

func proposalFindLabel(t *testing.T, root *protocol.Node, label string) *protocol.Node {
	t.Helper()
	found := findPublic(root, func(node *protocol.Node) bool {
		for _, candidateLabel := range node.Label {
			if candidateLabel == label {
				return true
			}
		}
		return false
	})
	if found == nil {
		t.Fatalf("public node with label %q not found", label)
	}
	return found
}

func proposalRequireSwipe(t *testing.T, harnessUnderTest *harness, expected swipeCall) {
	t.Helper()
	calls := swipeCalls(harnessUnderTest)
	if len(calls) != 1 || calls[0] != expected {
		t.Fatalf("swipes = %+v, want exactly %+v", calls, expected)
	}
}

func proposalRequireTap(t *testing.T, harnessUnderTest *harness, expected [2]int) {
	t.Helper()
	if len(harnessUnderTest.device.taps) != 1 ||
		harnessUnderTest.device.taps[0] != expected {
		t.Fatalf("taps = %v, want exactly one at %v",
			harnessUnderTest.device.taps, expected)
	}
}

func proposalRequireNoDispatch(t *testing.T, harnessUnderTest *harness) {
	t.Helper()
	if len(harnessUnderTest.device.taps) != 0 {
		t.Fatalf("taps = %v, want none", harnessUnderTest.device.taps)
	}
	if swipeCount(harnessUnderTest) != 0 {
		t.Fatalf("swipes = %+v, want none", swipeCalls(harnessUnderTest))
	}
}

func proposalRequireTargetChanged(t *testing.T, result *protocol.Result, reason string) {
	t.Helper()
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	status := result.HTTPStatus
	if status == 0 {
		status = protocol.Status(result.Error.Code)
	}
	if status != 409 {
		t.Fatalf("HTTP status = %d, want 409 (%+v)", status, result.Error)
	}
	if result.Error.Details == nil {
		t.Fatalf("target_changed carries no reason")
	}
	if got := result.Error.Details["reason"]; got != reason {
		t.Fatalf("target_changed reason = %#v, want %q (details %#v)",
			got, reason, result.Error.Details)
	}
}

func proposalScrollStackPage(display device.Display, bounds device.Rect) device.Capture {
	outer := proposalScrollNode(1, 0, 1, display, bounds, "android.widget.FrameLayout")
	middle := proposalScrollNode(2, 1, 2, display, bounds, "android.view.ViewGroup")
	inner := proposalScrollNode(3, 2, 3, display, bounds, "androidx.recyclerview.widget.RecyclerView")
	content := proposalNode(4, 3, 4, display,
		device.Rect{Left: 0, Top: 900, Right: 1080, Bottom: 1100},
		"android.widget.TextView", "stack-content")
	outer.Children = []int{2}
	middle.Children = []int{3}
	inner.Children = []int{4}
	return revalCapture(display, revalRoot(display.ID, 1), outer, middle, inner, content)
}

func TestProposalScrollCoextensiveStackOuterRefExecutes(t *testing.T) {
	bounds := device.Rect{Left: 0, Top: 363, Right: 1080, Bottom: 2265}
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		return proposalScrollStackPage(display, bounds)
	})
	harnessUnderTest := newHarness(t)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	scrollNodes := proposalScrollNodesByBounds(currentTree(t, harnessUnderTest.engine).Root, bounds)
	if len(scrollNodes) != 3 {
		t.Fatalf("published scroll stack = %d nodes, want 3", len(scrollNodes))
	}
	harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

	result := harnessUnderTest.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll",
		Container: &protocol.Target{
			UseRef: true, Ref: scrollNodes[0].Ref,
		},
		Direction: protocol.ScrollDirectionDown,
	})
	proposalRequireExecuted(t, result)
	proposalRequireSwipe(t, harnessUnderTest, swipeCall{
		displayID: 12, x1: 540, y1: 1771, x2: 540, y2: 858, durationMS: 400,
	})
}

func TestProposalScrollOverflowChildFallsBackWithoutSameViewport(t *testing.T) {
	outerBounds := device.Rect{Left: 300, Top: 900, Right: 780, Bottom: 1500}
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		outer := proposalScrollNode(1, 0, 1, display, outerBounds, "android.widget.FrameLayout")
		overflowChild := proposalScrollNode(2, 1, 2, display, screenRect(),
			"androidx.recyclerview.widget.RecyclerView")
		overflowChild.Children = []int{3}
		content := proposalNode(3, 2, 3, display,
			device.Rect{Left: 0, Top: 800, Right: 1080, Bottom: 1000},
			"android.widget.TextView", "overflow-content")
		outer.Children = []int{2}
		return revalCapture(display, revalRoot(display.ID, 1), outer, overflowChild, content)
	})
	harnessUnderTest := newHarness(t)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	outerNode := tapPublicNode(currentTree(t, harnessUnderTest.engine).Root,
		protocol.RoleScroll, outerBounds)
	if outerNode == nil {
		t.Fatal("outer scroll container not found")
	}
	harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

	result := harnessUnderTest.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll",
		Container: &protocol.Target{
			UseRef: true, Ref: outerNode.Ref,
		},
		Direction: protocol.ScrollDirectionDown,
	})
	proposalRequireExecuted(t, result)
	proposalRequireSwipe(t, harnessUnderTest, swipeCall{
		displayID: 12, x1: 540, y1: 1776, x2: 540, y2: 624, durationMS: 400,
	})
}

func TestProposalScrollAvoidsLocalTabAndHorizontalCard(t *testing.T) {
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		outer := proposalScrollNode(1, 0, 1, display, screenRect(), "android.widget.ScrollView")
		tabBar := proposalScrollNode(2, 1, 2, display,
			device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 220},
			"androidx.recyclerview.widget.RecyclerView")
		horizontalCard := proposalScrollNode(3, 1, 2, display,
			device.Rect{Left: 80, Top: 1000, Right: 1000, Bottom: 1180},
			"androidx.recyclerview.widget.RecyclerView")
		content := proposalNode(4, 1, 2, display,
			device.Rect{Left: 0, Top: 1300, Right: 1080, Bottom: 1500},
			"android.widget.TextView", "list-content")
		outer.Children = []int{2, 3, 4}
		return revalCapture(display, revalRoot(display.ID, 1),
			outer, tabBar, horizontalCard, content)
	})
	harnessUnderTest := newHarness(t)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	outerNode := tapPublicNode(currentTree(t, harnessUnderTest.engine).Root,
		protocol.RoleScroll, screenRect())
	if outerNode == nil {
		t.Fatal("outer scroll container not found")
	}
	harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

	result := harnessUnderTest.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll",
		Container: &protocol.Target{
			UseRef: true, Ref: outerNode.Ref,
		},
		Direction: protocol.ScrollDirectionDown,
	})
	proposalRequireExecuted(t, result)
	proposalRequireSwipe(t, harnessUnderTest, swipeCall{
		displayID: 12, x1: 540, y1: 2083, x2: 540, y2: 1498, durationMS: 400,
	})
}

func TestProposalScrollHalfScreenChildrenReturnNoSafeTrajectory(t *testing.T) {
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		outer := proposalScrollNode(1, 0, 1, display, screenRect(), "android.widget.ScrollView")
		topChild := proposalScrollNode(2, 1, 2, display,
			device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 1200},
			"androidx.recyclerview.widget.RecyclerView")
		bottomChild := proposalScrollNode(3, 1, 2, display,
			device.Rect{Left: 0, Top: 1200, Right: 1080, Bottom: 2400},
			"androidx.recyclerview.widget.RecyclerView")
		outer.Children = []int{2, 3}
		return revalCapture(display, revalRoot(display.ID, 1), outer, topChild, bottomChild)
	})
	harnessUnderTest := newHarness(t)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	outerNode := tapPublicNode(currentTree(t, harnessUnderTest.engine).Root,
		protocol.RoleScroll, screenRect())
	if outerNode == nil {
		t.Fatal("outer scroll container not found")
	}
	topNode := tapPublicNode(currentTree(t, harnessUnderTest.engine).Root,
		protocol.RoleScroll,
		device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 1200})
	bottomNode := tapPublicNode(currentTree(t, harnessUnderTest.engine).Root,
		protocol.RoleScroll,
		device.Rect{Left: 0, Top: 1200, Right: 1080, Bottom: 2400})
	if topNode == nil || bottomNode == nil {
		t.Fatal("half-screen scroll children not found")
	}
	capturesBefore := harnessUnderTest.device.captureCount()

	result := harnessUnderTest.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll",
		Container: &protocol.Target{
			UseRef: true, Ref: outerNode.Ref,
		},
		Direction: protocol.ScrollDirectionDown,
	})
	details := proposalRequireStaticReason(t, result,
		proposalNoSafeTrajectoryCode, "trajectory_blocked")
	proposalRequireDetailString(t, details, "scope", "target")
	proposalRequireDetailString(t, details, "scope_ref", outerNode.Ref)
	candidates := proposalCandidates(t, details)
	proposalRequireCandidateRefs(t, candidates, topNode.Ref, bottomNode.Ref)
	proposalRequireCandidateRelation(t, candidates, topNode.Ref, "child")
	proposalRequireCandidateRelation(t, candidates, bottomNode.Ref, "child")
	proposalRequireCandidateExecutable(t, candidates, topNode.Ref)
	proposalRequireCandidateExecutable(t, candidates, bottomNode.Ref)
	if harnessUnderTest.device.captureCount() != capturesBefore {
		t.Fatal("static no_safe_trajectory performed a capture")
	}
	if status := observationStatus(harnessUnderTest, start.Observation.ID); status != ObservationReady {
		t.Fatalf("observation status = %q, want ready", status)
	}
}

func TestProposalScrollBottomContainerUsesCenteredTrajectory(t *testing.T) {
	bounds := device.Rect{Left: 0, Top: 1458, Right: 1080, Bottom: 2400}
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		container := proposalScrollNode(1, 0, 1, display, bounds, "android.widget.ScrollView")
		return revalCapture(display, revalRoot(display.ID, 1), container)
	})
	harnessUnderTest := newHarness(t)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	container := tapPublicNode(currentTree(t, harnessUnderTest.engine).Root,
		protocol.RoleScroll, bounds)
	if container == nil {
		t.Fatal("bottom scroll container not found")
	}
	harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

	result := harnessUnderTest.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll",
		Container: &protocol.Target{
			UseRef: true, Ref: container.Ref,
		},
		Direction: protocol.ScrollDirectionDown,
		Amount:    protocol.ScrollAmountPage,
	})
	proposalRequireExecuted(t, result)
	proposalRequireSwipe(t, harnessUnderTest, swipeCall{
		displayID: 12, x1: 540, y1: 2155, x2: 540, y2: 1703, durationMS: 400,
	})
}

func TestProposalScrollFallsBackToQuarterAxisWithoutSnappingToCenter(t *testing.T) {
	pageStep := proposalCaptureStep(func(display device.Display) device.Capture {
		outer := proposalScrollNode(1, 0, 1, display, screenRect(), "android.widget.ScrollView")
		centerPillar := proposalScrollNode(2, 1, 2, display,
			device.Rect{Left: 500, Top: 0, Right: 580, Bottom: 2400},
			"androidx.recyclerview.widget.RecyclerView")
		outer.Children = []int{2}
		return revalCapture(display, revalRoot(display.ID, 1), outer, centerPillar)
	})
	harnessUnderTest := newHarness(t)
	harnessUnderTest.device.scriptCaptures(pageStep)
	start := harnessUnderTest.startSession()
	outerNode := tapPublicNode(currentTree(t, harnessUnderTest.engine).Root,
		protocol.RoleScroll, screenRect())
	if outerNode == nil {
		t.Fatal("outer scroll container not found")
	}
	harnessUnderTest.device.scriptCaptures(pageStep, pageStep)

	result := harnessUnderTest.actP(t, "", start.Observation.ID, protocol.Operation{
		Type: "scroll",
		Container: &protocol.Target{
			UseRef: true, Ref: outerNode.Ref,
		},
		Direction: protocol.ScrollDirectionDown,
	})
	proposalRequireExecuted(t, result)
	proposalRequireSwipe(t, harnessUnderTest, swipeCall{
		displayID: 12, x1: 270, y1: 1776, x2: 270, y2: 624, durationMS: 400,
	})
}

