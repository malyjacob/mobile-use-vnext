package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// visualGalleryCapture builds one gallery-like frame: an unlabelled,
// non-interactive image leaf covering two thirds of the target screen, followed by
// a short caption list below it. Every state flag of the image is reported as a
// known default, which both the classification and the compact crop require, and
// the caption rows sit outside the picture so they never make it a backdrop.
// Compact projection drops the picture entirely, so the visual hint is the only
// remaining evidence that the page is mostly imagery.
func visualGalleryCapture(display device.Display) device.Capture {
	screen := display.Bounds()
	imageBounds := device.Rect{
		Left: 0, Top: 0, Right: screen.Width(), Bottom: screen.Height() * 2 / 3,
	}
	captionRows := 10
	children := make([]int, 0, captionRows+1)
	for index := 0; index <= captionRows; index++ {
		children = append(children, index+1)
	}
	nodes := []device.RawNode{
		{
			Index: 0, Parent: -1, Children: children, Depth: 0,
			DisplayID: display.ID, Visible: true,
			RawBounds: screen, Bounds: screen,
			Package: "com.example", ClassName: "android.widget.FrameLayout",
			HasEnabled: true, Enabled: true,
		},
		{
			Index: 1, Parent: 0, Depth: 1, DisplayID: display.ID, Visible: true,
			RawBounds: imageBounds, Bounds: imageBounds,
			Package: "com.example", ClassName: "android.widget.ImageView",
			HasEnabled: true, Enabled: true,
			HasFocused: true, Focused: false,
			HasSelected: true, Selected: false,
			HasPassword: true, Password: false,
			HasChecked: true, Checked: false,
			HasClickable: true, Clickable: false,
		},
	}
	for index := 0; index < captionRows; index++ {
		top := imageBounds.Bottom + 40 + index*60
		rows := device.Rect{
			Left: 40, Top: top, Right: screen.Width() - 40, Bottom: top + 50,
		}
		nodes = append(nodes, device.RawNode{
			Index: index + 2, Parent: 0, Depth: 1, DisplayID: display.ID, Visible: true,
			RawBounds: rows, Bounds: rows,
			Package: "com.example", ClassName: "android.widget.TextView",
			Text: "cover-0" + string(rune('0'+index)), TextSet: true,
			HasEnabled: true, Enabled: true,
		})
	}
	return device.Capture{
		Display: display, Root: 0, Nodes: nodes,
		Foreground: device.Foreground{
			State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42),
		},
		CapturedAt: time.Now(),
	}
}

// visualGalleryStep scripts that frame as one capture.
func visualGalleryStep() captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return visualGalleryCapture(display), nil
	}
}

// visualGalleryHint is the token the fixed four-filter arithmetic yields for that
// frame: 1_728_000 of 2_592_000 visible pixels, rounded to 67%.
const visualGalleryHint = "image_heavy(67%)"

// visualTextPage is one text-only feed frame. Twelve labelled rows keep both the
// image branch (no image at all) and the sparse branch (far more than thirty
// runes) out, so the frame carries no hint.
func visualTextPage() []rawNodeSpec { return baselineRows("row", 12) }

// visualPollutedPage is one card whose source text interleaves zero-width
// padding. Only the public label is cleaned; the raw capture keeps its bytes.
func visualPollutedPage() ([]rawNodeSpec, string) {
	const raw = "个\u200b人\u200b闲\u200b置"
	specs := []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{
			Parent: 0, Class: "android.widget.TextView", Text: raw,
			Bounds: device.Rect{Left: 40, Top: 100, Right: 1040, Bottom: 220},
		},
	}
	return specs, raw
}

// TestObservationVisualStatesFullAndDeltaDedup pins the publication rule itself:
// a full tree states its own classification, while a delta repeats it when
// the kind is new, switched, or still loading.
func TestObservationVisualStatesFullAndDeltaDedup(t *testing.T) {
	heavy := visualHint{kind: "image_heavy", raw: visualGalleryHint}
	sparse := visualHint{kind: "sparse_tree", raw: "sparse_tree"}
	loading := visualHint{kind: "loading", raw: "loading"}
	media := visualHint{kind: "media_surface", raw: "media_surface"}
	for _, testCase := range []struct {
		name     string
		delta    bool
		hint     visualHint
		previous string
		want     string
	}{
		{"full states its own hint", false, heavy, "", heavy.raw},
		{"full states an unchanged kind again", false, heavy, "image_heavy", heavy.raw},
		{"full without a hint stays empty", false, visualHint{}, "sparse_tree", ""},
		{"delta suppresses an unchanged kind", true, heavy, "image_heavy", ""},
		{"delta suppresses unchanged media", true, media, media.kind, ""},
		{"delta repeats loading", true, loading, loading.kind, loading.raw},
		{"delta states a kind that appeared", true, heavy, "", heavy.raw},
		{"delta states a switched kind", true, sparse, "image_heavy", sparse.raw},
		{"delta without a hint stays empty", true, visualHint{}, "sparse_tree", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := observationVisual(testCase.delta, testCase.hint,
				testCase.previous); got != testCase.want {
				t.Fatalf("visual = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestObserveVisualHintOnlyOnStateChange drives the real publication funnel: the
// first frame is a full tree and states its hint, the unchanged follow-up frame is
// a delta and stays silent, and a forced full states the same hint again.
func TestObserveVisualHintOnlyOnStateChange(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	// The default page carries ten runes and three interactive controls, which is
	// exactly the sparse branch of the fixed classification.
	requireObservationCue(t, start.Observation, "sparse_tree", "", protocol.HintSnap)

	next := baselineObserve(t, h, "PvisualDelta", start.Observation.ID)
	if next.Tree == nil || next.Tree.Delta == nil {
		t.Fatalf("unchanged frame did not publish a delta: %+v", next.Tree)
	}
	requireObservationCue(t, next, "", "", "")

	forced, apiErr := h.engine.Observe(context.Background(), "agent", "PvisualFull",
		&protocol.ObserveRequest{Reference: next.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("forced full rejected: %v", apiErr)
	}
	if forced.Observation.Tree == nil || forced.Observation.Tree.Full == nil {
		t.Fatalf("full=true did not publish a full tree: %+v", forced.Observation.Tree)
	}
	requireObservationCue(t, forced.Observation, "sparse_tree", "", protocol.HintSnap)
}

// TestObserveVisualHintOnFullTree covers the imagery branch end to end and the
// delta that follows it: the full frame states the exact token, the unchanged
// delta suppresses it.
func TestObserveVisualHintOnFullTree(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(visualGalleryStep())
	start := h.startSession()
	if start.Observation.Tree == nil || start.Observation.Tree.Full == nil {
		t.Fatalf("start did not publish a full tree")
	}
	if got := start.Observation.Visual; got != visualGalleryHint {
		t.Fatalf("start visual = %q, want %q", got, visualGalleryHint)
	}

	next := baselineObserve(t, h, "PvisualGalleryDelta", start.Observation.ID)
	if next.Tree == nil || next.Tree.Delta == nil {
		t.Fatalf("unchanged gallery frame did not publish a delta: %+v", next.Tree)
	}
	if next.Visual != "" {
		t.Fatalf("gallery delta repeated the hint: %q", next.Visual)
	}
}

// TestObserveBackupBaselineDeltaReportsSwitchedHint proves the dedup compares
// against the immediately previous published frame, not against the frame the
// delta is expressed against. Here the previous frame carries no hint while the
// selected delta base is itself an image-heavy frame: the switched state must be
// reported even though that base already recorded the same kind.
func TestObserveBackupBaselineDeltaReportsSwitchedHint(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(visualGalleryStep())
	gallery := h.startSession().Observation
	if gallery.Visual != visualGalleryHint {
		t.Fatalf("gallery start visual = %q, want %q", gallery.Visual, visualGalleryHint)
	}

	// A forced full of another, hint-free page retires the active baseline and
	// keeps the gallery frame as a backup of the pool.
	h.device.scriptCaptures(baselinePageStep(visualTextPage()))
	listResult, apiErr := h.engine.Observe(context.Background(), "agent", "PvisualList",
		&protocol.ObserveRequest{Reference: gallery.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("list full rejected: %v", apiErr)
	}
	list := listResult.Observation
	if list.Tree == nil || list.Tree.Full == nil {
		t.Fatalf("list frame was not published as a full tree: %+v", list.Tree)
	}
	if list.Visual != "" {
		t.Fatalf("hint-free page published a visual: %q", list.Visual)
	}

	// The gallery page returns. Its cheap delta is expressed against the backup
	// gallery full, while the hint must be compared with the list frame.
	h.device.scriptCaptures(visualGalleryStep())
	back := baselineObserve(t, h, "PvisualBack", list.ID)
	if back.Tree == nil || back.Tree.Delta == nil {
		t.Fatalf("page return did not publish a delta: %+v", back.Tree)
	}
	if back.Tree.Delta.Base != gallery.ID {
		t.Fatalf("delta base = %q, want the backup gallery full %q",
			back.Tree.Delta.Base, gallery.ID)
	}
	if back.Visual != visualGalleryHint {
		t.Fatalf("switched hint on a backup-baseline delta = %q, want %q",
			back.Visual, visualGalleryHint)
	}
}

// TestFailedTreeObservationCarriesNoVisual pins that a failed acquisition never
// invents the visual classification of a tree it did not publish.
func TestFailedTreeObservationCarriesNoVisual(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.device.scriptCaptures(failingCapture())
	result, apiErr := h.engine.Observe(context.Background(), "agent", "PvisualFailed",
		&protocol.ObserveRequest{Reference: start.Observation.ID})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	if result.Observation == nil || result.Observation.Status != "failed" {
		t.Fatalf("failed acquisition = %+v", result.Observation)
	}
	requireObservationCue(t, result.Observation, "", "", "")
}

// TestPartialObservationKeepsTreeVisual pins that the hint belongs to the tree
// fact: a published tree stays classified even when the requested image of the
// same acquisition could not be attached.
func TestPartialObservationKeepsTreeVisual(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	result, apiErr := h.engine.Observe(context.Background(), "agent", "PvisualPartial",
		&protocol.ObserveRequest{Reference: start.Observation.ID, Full: true, Image: true})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	observation := result.Observation
	if observation == nil || observation.Status != "partial" || observation.Tree == nil ||
		observation.Tree.Full == nil {
		t.Fatalf("partial acquisition = %+v", observation)
	}
	if observation.Image == nil || observation.Image.Error == nil {
		t.Fatalf("partial acquisition lost its image failure: %+v", observation.Image)
	}
	requireObservationCue(t, observation, "sparse_tree", "", protocol.HintSnap)
}

// TestWaitForQueryMatchesPurifiedPublicLabel proves the query contract of the
// condition wait: the literal query is matched against the cleaned public label,
// while the captured source text stays exactly as the device reported it, so act
// revalidation and text read-back keep their original evidence.
func TestWaitForQueryMatchesPurifiedPublicLabel(t *testing.T) {
	h, reference := waitForTestHarness(t)
	specs, raw := visualPollutedPage()
	h.device.scriptCaptures(baselinePageStep(specs))
	result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
		&protocol.WaitForRequest{
			Reference: reference, Query: "个人闲置", HasQuery: true, Timeout: 10,
		})
	if apiErr != nil {
		t.Fatalf("wait_for rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("wait_for failed: %+v", result.Error)
	}
	if result.Wait == nil || result.Wait.Trigger != protocol.WaitTriggerQuery {
		t.Fatalf("trigger = %+v, want the literal query hit", result.Wait)
	}

	requireObservationCue(t, result.Observation, "sparse_tree", "", protocol.HintSnap)

	h.engine.mu.Lock()
	current := h.engine.current
	h.engine.mu.Unlock()
	if current == nil || current.Projection == nil || current.Projection.Tree == nil {
		t.Fatal("query hit published no public tree")
	}
	label := publicLabelOf(current.Projection.Tree.Root, "个人闲置")
	if label == "" {
		t.Fatalf("public tree did not expose the cleaned label")
	}
	if strings.Contains(label, "\u200b") {
		t.Fatalf("public label kept a zero-width character")
	}
	if current.Projection.Capture == nil {
		t.Fatal("public projection lost its source capture")
	}
	rawKept := false
	for _, node := range current.Projection.Capture.Nodes {
		if node.Text == raw {
			rawKept = true
			break
		}
	}
	if !rawKept {
		t.Fatalf("source capture text was rewritten instead of only re-projected")
	}

	// The same literal query addresses the retained history through inspect, so
	// the cleaned label is the one query surface both tools share.
	inspection, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		Reference: current.ID, Query: "个人闲置", HasQuery: true,
	})
	if apiErr != nil {
		t.Fatalf("inspect query rejected: %v", apiErr)
	}
	if inspection.Inspection == nil || inspection.Inspection.Total < 1 ||
		len(inspection.Inspection.Matches) == 0 {
		t.Fatalf("inspect query did not match the cleaned label: %+v", inspection.Inspection)
	}
}

// publicLabelOf returns the first public label that contains the folded needle.
func publicLabelOf(root *protocol.Node, needle string) string {
	if root == nil {
		return ""
	}
	for _, value := range root.Label {
		if strings.Contains(value, needle) {
			return value
		}
	}
	for _, child := range root.Children {
		if found := publicLabelOf(child, needle); found != "" {
			return found
		}
	}
	return ""
}
