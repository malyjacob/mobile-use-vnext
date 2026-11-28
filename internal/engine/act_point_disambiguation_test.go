package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The F1 fixtures below describe a card whose public subtree covers the point
// through one passive label and through one inner button of the same public
// depth:
//
//	card  [100,300,900,600]  tap (outer owner)
//	note  [120,320,560,560]  passive   -> owner card
//	open  [400,380,700,480]  tap       -> owner open, nested inside the card
//
// Both deepest hits sit at depth 2, so the point resolves to two nested
// interactive owners. The innermost admission rule must pick exactly that inner
// owner while two unrelated siblings still stay ambiguous.
var (
	pointDisambiguationCard  = device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 600}
	pointDisambiguationNote  = device.Rect{Left: 120, Top: 320, Right: 560, Bottom: 560}
	pointDisambiguationInner = device.Rect{Left: 400, Top: 380, Right: 700, Bottom: 480}
	pointDisambiguationCover = device.Rect{Left: 350, Top: 390, Right: 750, Bottom: 470}
	pointDisambiguationPoint = [2]int{500, 420}
)

func pointDisambiguationRoot(display device.Display, children ...int) device.RawNode {
	screen := device.Rect{Left: 0, Top: 0, Right: display.Width, Bottom: display.Height}
	return device.RawNode{
		Index: 0, Parent: -1, Children: children, Depth: 0, DisplayID: display.ID,
		Visible: true, RawBounds: screen, Bounds: screen,
		Package: "com.example", ClassName: "android.widget.FrameLayout",
		HasEnabled: true, Enabled: true,
	}
}

func pointDisambiguationCapture(display device.Display, nodes []device.RawNode) device.Capture {
	return device.Capture{
		Display: display, Root: 0, Nodes: nodes, CapturedAt: time.Now(),
		Foreground: device.Foreground{
			State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42),
		},
	}
}

// pointDisambiguationNestedPage builds the nested card fixture. mutate may add
// capability variants (for example long_press on both owners).
func pointDisambiguationNestedPage(display device.Display,
	mutate func(*device.Capture)) device.Capture {
	card := tapRawNode(1, 0, 1, display.ID, pointDisambiguationCard,
		"android.widget.FrameLayout", "", true, false)
	card.Children = []int{2, 3}
	note := tapRawNode(2, 1, 2, display.ID, pointDisambiguationNote,
		"android.widget.TextView", "card note", false, false)
	open := tapRawNode(3, 1, 2, display.ID, pointDisambiguationInner,
		"android.widget.Button", "Open", true, false)
	capture := pointDisambiguationCapture(display, []device.RawNode{
		pointDisambiguationRoot(display, 1), card, note, open,
	})
	if mutate != nil {
		mutate(&capture)
	}
	return capture
}

func pointDisambiguationNestedStep(mutate func(*device.Capture)) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return pointDisambiguationNestedPage(display, mutate), nil
	}
}

// pointDisambiguationTextPage nests the inner button inside an input: the point
// then has two owners, but only the outer input carries ActionText.
func pointDisambiguationTextPage(display device.Display) device.Capture {
	input := tapRawNode(1, 0, 1, display.ID, pointDisambiguationCard,
		"android.widget.EditText", "", true, false)
	input.Editable = true
	input.TextSet = true
	input.HasPassword = true
	input.Password = false
	input.HasFocused = true
	input.Focused = true
	input.Children = []int{2, 3}
	note := tapRawNode(2, 1, 2, display.ID, pointDisambiguationNote,
		"android.widget.TextView", "card note", false, false)
	open := tapRawNode(3, 1, 2, display.ID, pointDisambiguationInner,
		"android.widget.Button", "Open", true, false)
	return pointDisambiguationCapture(display, []device.RawNode{
		pointDisambiguationRoot(display, 1), input, note, open,
	})
}

func pointDisambiguationTextStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return pointDisambiguationTextPage(display), nil
	}
}

// pointDisambiguationTextTargetDevice records the exact target of every text
// write so a test can prove which input received the text. The embedded fake
// device keeps the ordinary capture and text behavior.
type pointDisambiguationTextTargetDevice struct {
	*fakeDevice
	textTargets []device.TextTarget
}

func (d *pointDisambiguationTextTargetDevice) SetTextOnDisplay(ctx context.Context,
	target device.TextTarget, text string) (device.TextResult, error) {
	d.textTargets = append(d.textTargets, target)
	return d.fakeDevice.SetTextOnDisplay(ctx, target, text)
}

// pointDisambiguationNestedInputsPage nests one editable input inside another.
// Both owners are public RoleInput nodes that carry ActionText and both cover
// the point, so the text matching set itself is nested and only the innermost
// reduction can admit a unique owner.
func pointDisambiguationNestedInputsPage(display device.Display) device.Capture {
	outer := tapRawNode(1, 0, 1, display.ID, pointDisambiguationCard,
		"android.widget.EditText", "", true, false)
	outer.Editable = true
	outer.TextSet = true
	outer.HasPassword = true
	outer.Password = false
	outer.Children = []int{2, 3}
	note := tapRawNode(2, 1, 2, display.ID, pointDisambiguationNote,
		"android.widget.TextView", "outer hint", false, false)
	inner := tapRawNode(3, 1, 2, display.ID, pointDisambiguationInner,
		"android.widget.EditText", "", true, false)
	inner.Editable = true
	inner.TextSet = true
	inner.HasPassword = true
	inner.Password = false
	inner.HasFocused = true
	inner.Focused = true
	return pointDisambiguationCapture(display, []device.RawNode{
		pointDisambiguationRoot(display, 1), outer, note, inner,
	})
}

func pointDisambiguationNestedInputsStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return pointDisambiguationNestedInputsPage(display), nil
	}
}

// pointDisambiguationSiblingStep publishes two unrelated buttons covering the
// same point at the same depth. Neither is an ancestor of the other, so the
// point stays an honest ambiguity.
func pointDisambiguationSiblingStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		left := tapRawNode(1, 0, 1, display.ID, pointDisambiguationCard,
			"android.widget.Button", "left", true, false)
		right := tapRawNode(2, 0, 1, display.ID, pointDisambiguationInner,
			"android.widget.Button", "right", true, false)
		return pointDisambiguationCapture(display, []device.RawNode{
			pointDisambiguationRoot(display, 1, 2), left, right,
		}), nil
	}
}

// pointDisambiguationAmbiguousStep adds one unrelated matching button as a third
// depth-2 child of the card. The point then resolves to the nested pair plus one
// sibling, so the ancestor is reduced away and two honest competitors remain.
func pointDisambiguationAmbiguousStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		capture := pointDisambiguationNestedPage(display, nil)
		third := tapRawNode(4, 1, 2, display.ID,
			device.Rect{Left: 420, Top: 400, Right: 780, Bottom: 470},
			"android.widget.Button", "third", true, false)
		capture.Nodes = append(capture.Nodes, third)
		capture.Nodes[1].Children = append(capture.Nodes[1].Children, 4)
		return capture, nil
	}
}

// pointDisambiguationParentNode returns the direct published parent of child.
func pointDisambiguationParentNode(node, child *protocol.Node) *protocol.Node {
	if node == nil || child == nil {
		return nil
	}
	for _, candidate := range node.Children {
		if candidate == child {
			return node
		}
		if parent := pointDisambiguationParentNode(candidate, child); parent != nil {
			return parent
		}
	}
	return nil
}

// pointDisambiguationChangedStep renames the passive note and adds one
// independent root-level clickable cover over the point. The hit set keeps its
// size and the nested owners, so the fresh proof fails on identity while the
// old frame still has exactly one innermost owner: only a reduced expected owner
// can name the external cover as a blocking ref.
func pointDisambiguationChangedStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		capture := pointDisambiguationNestedPage(display, nil)
		capture.Nodes[2].Text = "changed note"
		capture.Nodes[2].TextSet = true
		cover := tapRawNode(4, 0, 1, display.ID, pointDisambiguationCover,
			"android.widget.Button", "cover", true, false)
		capture.Nodes = append(capture.Nodes, cover)
		capture.Nodes[0].Children = append(capture.Nodes[0].Children, 4)
		return capture, nil
	}
}

func pointDisambiguationOperation(kind string) protocol.Operation {
	return protocol.Operation{
		Type: kind,
		Target: &protocol.Target{
			X: pointDisambiguationPoint[0], Y: pointDisambiguationPoint[1],
		},
	}
}

// TestActCoordinatePointDisambiguatesNestedOwners is the forward F1 case: the
// coordinate hits both the labelled outer card and the inner button, static
// admission passes with mode point_disambiguated (silently, without
// auto_recovery), and the fresh frame proves the same nested owner set.
func TestActCoordinatePointDisambiguatesNestedOwners(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		longPress bool
	}{
		{name: "tap", kind: "tap"},
		{name: "long_press", kind: "long_press", longPress: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var buffer bytes.Buffer
			logger := eventlog.New(&buffer, "instance-test")
			h := newHarnessWithLogger(t, true, logger)
			mutate := func(capture *device.Capture) {
				if !testCase.longPress {
					return
				}
				capture.Nodes[1].HasLongClick = true
				capture.Nodes[1].LongClickable = true
				capture.Nodes[3].HasLongClick = true
				capture.Nodes[3].LongClickable = true
			}
			h.device.scriptCaptures(pointDisambiguationNestedStep(mutate))
			start := h.startSession()

			result := h.actCoordinate(t, start.Observation.ID,
				pointDisambiguationOperation(testCase.kind))
			if result.Outcome != OutcomeExecuted || result.Error != nil {
				t.Fatalf("outcome = %s error %+v, want executed", result.Outcome, result.Error)
			}
			if result.AutoRecovery != nil {
				t.Fatalf("static disambiguation wrote auto_recovery: %+v", result.AutoRecovery)
			}
			if len(h.device.taps) != 1 || h.device.taps[0] != pointDisambiguationPoint {
				t.Fatalf("taps = %v, want one dispatch at the requested point", h.device.taps)
			}
			logger.Close()
			recorded := buffer.String()
			for _, want := range []string{
				"act.point", "proven=true", `mode="point_disambiguated"`,
			} {
				if !strings.Contains(recorded, want) {
					t.Fatalf("event log lacks %q:\n%s", want, recorded)
				}
			}
		})
	}
}

// TestActCoordinatePointDisambiguatesOnlyMatchingOwners is the matching-set
// control: the inner owner only carries long_press, so a tap has exactly one
// matching owner and keeps the pre-F1 behavior. Reducing the unfiltered owner
// set first would wrongly select the inner button.
func TestActCoordinatePointDisambiguatesOnlyMatchingOwners(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(pointDisambiguationNestedStep(func(capture *device.Capture) {
		capture.Nodes[3].HasClickable = false
		capture.Nodes[3].Clickable = false
		capture.Nodes[3].HasLongClick = true
		capture.Nodes[3].LongClickable = true
	}))
	start := h.startSession()

	result := h.actCoordinate(t, start.Observation.ID, pointDisambiguationOperation("tap"))
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome = %s error %+v, want executed", result.Outcome, result.Error)
	}
	if len(h.device.taps) != 1 || h.device.taps[0] != pointDisambiguationPoint {
		t.Fatalf("taps = %v, want one dispatch at the requested point", h.device.taps)
	}
}

// TestActCoordinatePointTextReducesOnlyMatchingOwners is the text reverse
// control: both owners cover the point, but the inner button has no ActionText,
// so the text matching set keeps the outer input. The write must land in that
// input and never be redirected to the non-editable inner owner.
func TestActCoordinatePointTextReducesOnlyMatchingOwners(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	h.device.scriptCaptures(pointDisambiguationTextStep())
	start := h.startSession()

	operation := pointDisambiguationOperation("text")
	operation.Text = "hello"
	result := h.actCoordinate(t, start.Observation.ID, operation)
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome = %s error %+v, want executed", result.Outcome, result.Error)
	}
	if len(h.device.texts) != 1 || h.device.texts[0] != "hello" {
		t.Fatalf("texts = %v, want one write to the matched input", h.device.texts)
	}
	if len(h.device.taps) != 1 || h.device.taps[0] != pointDisambiguationPoint {
		t.Fatalf("taps = %v, want the focus tap at the requested point", h.device.taps)
	}
	logger.Close()
	if recorded := buffer.String(); !strings.Contains(recorded, `mode="point_disambiguated"`) {
		t.Fatalf("event log lacks the disambiguated text mode:\n%s", recorded)
	}
}

// TestActCoordinatePointDisambiguatesNestedInputs is the text F1 forward case:
// the coordinate hits two nested public inputs that both carry ActionText, so
// the text matching set is nested too and only its innermost member may be
// admitted. The write must land in that inner input exactly once.
func TestActCoordinatePointDisambiguatesNestedInputs(t *testing.T) {
	var buffer bytes.Buffer
	logger := eventlog.New(&buffer, "instance-test")
	h := newHarnessWithLogger(t, true, logger)
	recorder := &pointDisambiguationTextTargetDevice{fakeDevice: h.device}
	h.engine.dev = recorder
	h.device.scriptCaptures(pointDisambiguationNestedInputsStep())
	start := h.startSession()

	operation := pointDisambiguationOperation("text")
	operation.Text = "hello"
	result := h.actCoordinate(t, start.Observation.ID, operation)
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("outcome = %s error %+v, want executed", result.Outcome, result.Error)
	}
	if result.AutoRecovery != nil {
		t.Fatalf("static nested-input disambiguation wrote auto_recovery: %+v", result.AutoRecovery)
	}
	if len(recorder.textTargets) != 1 || recorder.textTargets[0].Bounds != pointDisambiguationInner {
		t.Fatalf("text targets = %+v, want one write to the inner input bounds",
			recorder.textTargets)
	}
	if len(h.device.texts) != 1 || h.device.texts[0] != "hello" {
		t.Fatalf("texts = %v, want one write to the nested inner input", h.device.texts)
	}
	if len(h.device.taps) != 1 || h.device.taps[0] != pointDisambiguationPoint {
		t.Fatalf("taps = %v, want the focus tap at the requested point", h.device.taps)
	}
	logger.Close()
	recorded := buffer.String()
	if !strings.Contains(recorded, "act.point") ||
		!strings.Contains(recorded, `mode="point_disambiguated"`) {
		t.Fatalf("event log lacks the disambiguated text admission:\n%s", recorded)
	}
}

// TestActCoordinatePointKeepsSiblingOwnersAmbiguous is the reverse control of
// F1: two unrelated buttons that are neither ancestor nor descendant of each
// other still reject before claim with the documented competing-target error
// and both competing candidates.
func TestActCoordinatePointKeepsSiblingOwnersAmbiguous(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(pointDisambiguationSiblingStep())
	start := h.startSession()
	captures := h.device.captureCount()

	result := h.actCoordinate(t, start.Observation.ID, pointDisambiguationOperation("tap"))
	requireNotExecuted(t, result, ambiguousPointCode)
	details := result.Error.Details
	if details["reason"] != staticReasonCompetingTargets ||
		details["scope"] != tapStaticScopePoint {
		t.Fatalf("ambiguous_point details = %#v, want competing_targets/point", details)
	}
	candidates, ok := details["candidates"].([]map[string]any)
	if !ok || len(candidates) != 2 {
		t.Fatalf("ambiguous_point candidates = %#v, want 2 competing owners", details["candidates"])
	}
	if len(h.device.taps) != 0 {
		t.Fatalf("input was dispatched: %v", h.device.taps)
	}
	if got := h.device.captureCount(); got != captures {
		t.Fatalf("static rejection performed %d new capture(s)", got-captures)
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("observation status = %q after static rejection, want ready", status)
	}
}

// TestActCoordinatePointAmbiguousListsInnermostOnly keeps F1 honest: with the
// nested pair plus one unrelated sibling the point is still ambiguous, and the
// candidates name only the two innermost owners. The reduced ancestor must not
// reappear as a competing candidate.
func TestActCoordinatePointAmbiguousListsInnermostOnly(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(pointDisambiguationAmbiguousStep())
	start := h.startSession()
	captures := h.device.captureCount()

	result := h.actCoordinate(t, start.Observation.ID, pointDisambiguationOperation("tap"))
	requireNotExecuted(t, result, ambiguousPointCode)
	details := result.Error.Details
	if details["reason"] != staticReasonCompetingTargets ||
		details["scope"] != tapStaticScopePoint {
		t.Fatalf("ambiguous_point details = %#v, want competing_targets/point", details)
	}
	candidates, ok := details["candidates"].([]map[string]any)
	if !ok || len(candidates) != 2 {
		t.Fatalf("ambiguous_point candidates = %#v, want the 2 innermost owners",
			details["candidates"])
	}
	root := currentTree(t, h.engine).Root
	inner := mustFindNodeByTextOrResource(t, root, "Open")
	third := mustFindNodeByTextOrResource(t, root, "third")
	if inner == nil || third == nil {
		t.Fatalf("published frame lacks the competing owners: inner=%v third=%v",
			inner, third)
	}
	listed := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		ref, _ := candidate["ref"].(string)
		listed[ref] = true
	}
	if len(listed) != 2 || !listed[inner.Ref] || !listed[third.Ref] {
		t.Fatalf("candidate refs = %v, want only %s and %s", listed, inner.Ref, third.Ref)
	}
	ancestor := pointDisambiguationParentNode(root, inner)
	if ancestor == nil {
		t.Fatal("nested inner owner has no published parent")
	}
	if listed[ancestor.Ref] {
		t.Fatalf("reduced ancestor %s listed as a candidate: %v", ancestor.Ref, listed)
	}
	if len(h.device.taps) != 0 {
		t.Fatalf("input was dispatched: %v", h.device.taps)
	}
	if got := h.device.captureCount(); got != captures {
		t.Fatalf("static rejection performed %d new capture(s)", got-captures)
	}
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("observation status = %q after static rejection, want ready", status)
	}
}

// TestActCoordinatePointChangedReportsNestedBlockingRefs proves the diagnostics
// alignment: the old frame's owners are nested, so only the reduced innermost
// owner can be matched on the fresh frame and name the external cover as the
// blocking ref. Without the reduction the expected owner is unknown and the
// point_changed rejection carries no blocking_refs.
func TestActCoordinatePointChangedReportsNestedBlockingRefs(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(pointDisambiguationNestedStep(nil))
	start := h.startSession()
	changed := pointDisambiguationChangedStep()
	h.device.scriptCaptures(changed, changed)

	result := h.actCoordinate(t, start.Observation.ID, pointDisambiguationOperation("tap"))
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	if reason := result.Error.Details["reason"]; reason != targetChangedReasonPointChanged {
		t.Fatalf("point_changed reason = %#v", result.Error.Details)
	}
	if result.Observation == nil {
		t.Fatalf("point_changed rejection delivered no observation: %+v", result.Error)
	}
	if len(h.device.taps) != 0 {
		t.Fatalf("input was dispatched: %v", h.device.taps)
	}
	cover := mustFindNodeByTextOrResource(t, currentTree(t, h.engine).Root, "cover")
	if cover == nil {
		t.Fatal("published frame lacks the cover node")
	}
	requireDetailStringSlice(t, result.Error.Details, "blocking_refs", cover.Ref)
}

// TestInnermostPointOwnersKeepsStrictInnermostOwners states the helper contract
// directly: only strict public descendants reduce an owner, unrelated owners
// keep their relative order, and a corrupt parent cycle fails closed instead of
// looping.
func TestInnermostPointOwnersKeepsStrictInnermostOwners(t *testing.T) {
	outer := &protocol.Node{Ref: "n1"}
	middle := &protocol.Node{Ref: "n2"}
	inner := &protocol.Node{Ref: "n3"}
	firstSibling := &protocol.Node{Ref: "n4"}
	secondSibling := &protocol.Node{Ref: "n5"}
	view := &tapPublicView{info: map[*protocol.Node]*tapPublicInfo{
		outer:         {node: outer},
		middle:        {node: middle, parent: outer},
		inner:         {node: inner, parent: middle},
		firstSibling:  {node: firstSibling, parent: outer},
		secondSibling: {node: secondSibling, parent: outer},
	}}
	cases := []struct {
		name   string
		owners []*protocol.Node
		want   []*protocol.Node
	}{
		{name: "nested chain", owners: []*protocol.Node{outer, middle, inner},
			want: []*protocol.Node{inner}},
		{name: "relative order", owners: []*protocol.Node{firstSibling, middle, inner},
			want: []*protocol.Node{firstSibling, inner}},
		{name: "unrelated siblings stay", owners: []*protocol.Node{firstSibling, secondSibling},
			want: []*protocol.Node{firstSibling, secondSibling}},
		{name: "single owner", owners: []*protocol.Node{middle},
			want: []*protocol.Node{middle}},
		{name: "empty set", owners: nil, want: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := innermostPointOwners(view, testCase.owners)
			if len(got) != len(testCase.want) {
				t.Fatalf("innermost = %v, want %v", got, testCase.want)
			}
			for index := range testCase.want {
				if got[index] != testCase.want[index] {
					t.Fatalf("innermost[%d] = %v, want %v", index, got[index], testCase.want[index])
				}
			}
		})
	}

	cycleFirst := &protocol.Node{Ref: "n6"}
	cycleSecond := &protocol.Node{Ref: "n7"}
	cycleView := &tapPublicView{info: map[*protocol.Node]*tapPublicInfo{
		cycleFirst:  {node: cycleFirst, parent: cycleSecond},
		cycleSecond: {node: cycleSecond, parent: cycleFirst},
	}}
	if got := innermostPointOwners(cycleView,
		[]*protocol.Node{cycleFirst, cycleSecond}); len(got) != 0 {
		t.Fatalf("cyclic owners = %v, want a fail-closed empty reduction", got)
	}

	if got := innermostPointOwners(nil, []*protocol.Node{outer, inner}); len(got) != 2 ||
		got[0] != outer || got[1] != inner {
		t.Fatalf("nil view = %v, want every owner in input order", got)
	}
}
