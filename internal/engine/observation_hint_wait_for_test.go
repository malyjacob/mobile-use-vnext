package engine

import (
	"context"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// sparseActPage is one frame of the calibrated sparse branch: six readable runes
// and a single interactive control. textLeft moves one passive text row, whose
// rectangle the compact projection hides, so the follow-up frame differs only in
// the full tree and publishes detail_only with an empty delta.
func sparseActPage(textLeft int) []rawNodeSpec {
	return []rawNodeSpec{
		{Parent: -1, Bounds: screenRect(), Class: "android.widget.FrameLayout"},
		{
			Parent: 0, Bounds: device.Rect{Left: 40, Top: 1800, Right: 400, Bottom: 1900},
			Class: "android.widget.Button", Text: "Go", Clickable: true,
		},
		{
			Parent: 0, Bounds: device.Rect{Left: textLeft, Top: 400, Right: 1000, Bottom: 480},
			Class: "android.widget.TextView", Text: "note",
		},
	}
}

// TestObserveFirstSparseFrameWithImageRoutesWaitFor pins the first publication of
// a sparse page: an attached valid image never turns the follow-up into a second
// screenshot, so the recommendation is the condition wait. The full read keeps the
// sparse kind published instead of deduplicating it against the start frame.
func TestObserveFirstSparseFrameWithImageRoutesWaitFor(t *testing.T) {
	h, _ := imageTestHarness(t)
	h.engine.cfg.Settle = SettlePolicies{}
	start := h.startSession()
	setFakeImageSource(h, time.Now())

	result, apiErr := h.engine.Observe(context.Background(), "agent", "PsparseImage",
		&protocol.ObserveRequest{Reference: start.Observation.ID, Full: true, Image: true})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	obs := result.Observation
	if obs == nil || obs.Status != "complete" || obs.Image == nil || obs.Image.Error != nil {
		t.Fatalf("observe published no valid image: %+v", obs)
	}
	requireFullTree(t, obs, 2)
	requireObservationCue(t, obs, observation.VisualKindSparseTree, "", protocol.HintWaitFor)
}

// TestActSparseDetailOnlyKeepsSnap covers the follow-up frame of an act whose
// compact content did not change: the sparse classification is deduplicated on
// the delta, while the tree change still routes the hint, and an attached image
// suppresses the screenshot.
func TestActSparseDetailOnlyKeepsSnap(t *testing.T) {
	for _, tc := range []struct {
		name     string
		image    bool
		wantHint string
	}{
		{"without image keeps snap", false, protocol.HintSnap},
		{"with image suppresses snap", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := imageTestHarness(t)
			h.engine.cfg.Settle = SettlePolicies{}
			h.device.scriptCaptures(baselinePageStep(sparseActPage(40)))
			start := h.startSession()
			requireObservationCue(t, start.Observation, observation.VisualKindSparseTree, "",
				protocol.HintWaitFor)

			button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
			if button == nil {
				t.Fatal("button not found in tree")
			}
			h.device.scriptCaptures(
				baselinePageStep(sparseActPage(40)), baselinePageStep(sparseActPage(60)))
			if tc.image {
				setFakeImageSource(h, time.Now())
			}
			result := h.actWith(t, "PsparseDetail", &protocol.ActRequest{
				Reference: start.Observation.ID,
				Operation: protocol.Operation{
					Type: "tap", Target: &protocol.Target{UseRef: true, Ref: button.Ref},
				},
				Image: tc.image,
			})
			if result.Outcome != OutcomeExecuted || result.Observation == nil {
				t.Fatalf("act = %q, observation = %+v", result.Outcome, result.Observation)
			}
			delta := requireBaselineDelta(t, result.Observation, 2, start.Observation.ID)
			if !delta.IsEmpty() {
				t.Fatalf("detail-only frame published a nonempty delta: %+v", delta)
			}
			// The sparse kind is unchanged, so the delta omits it; the hint is
			// derived from the tree change alone.
			requireObservationCue(t, result.Observation, "",
				protocol.TreeChangeDetailOnly, tc.wantHint)
		})
	}
}

// TestWaitForTerminalSparseFrameKeepsVisual pins the anti-loop contract of the
// condition wait: a terminal frame that is still sparse keeps visual=sparse_tree
// even though the delta and the kind are unchanged, and without an image the
// follow-up becomes one read-only screenshot.
//
// Reverse control: deleting the fromWaitFor retention branch in observationVisual
// makes the same delta suppress the kind, so both the visual and the derived hint
// disappear and this test fails.
func TestWaitForTerminalSparseFrameKeepsVisual(t *testing.T) {
	for _, tc := range []struct {
		name     string
		image    bool
		wantHint string
	}{
		{"without image routes snap", false, protocol.HintSnap},
		{"with image keeps no hint", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := imageTestHarness(t)
			h.engine.cfg.Wait = fastWaitPolicy()
			start := h.startSession()
			requireObservationCue(t, start.Observation, observation.VisualKindSparseTree, "",
				protocol.HintWaitFor)
			setFakeImageSource(h, time.Now())

			result, apiErr := h.engine.WaitFor(context.Background(), "agent", "",
				&protocol.WaitForRequest{
					Reference: start.Observation.ID, Timeout: 10, Stable: true,
					Image: tc.image,
				})
			if apiErr != nil {
				t.Fatalf("wait_for rejected: %v", apiErr)
			}
			if result.Error != nil || result.Wait == nil ||
				result.Wait.Trigger != protocol.WaitTriggerStable {
				t.Fatalf("wait_for = %+v, error = %+v", result.Wait, result.Error)
			}
			if tc.image {
				if result.Observation.Image == nil || result.Observation.Image.Error != nil {
					t.Fatalf("wait_for published no valid image: %+v", result.Observation.Image)
				}
			}
			delta := requireBaselineDelta(t, result.Observation, 2, start.Observation.ID)
			if !delta.IsEmpty() {
				t.Fatalf("stable terminal frame published a nonempty delta: %+v", delta)
			}
			requireObservationCue(t, result.Observation, observation.VisualKindSparseTree, "",
				tc.wantHint)
		})
	}
}

// TestObservationHintInvariants sweeps the whole routing matrix. A hint never
// appears without a published cue, a valid image never asks for a screenshot, and
// the value stays inside the closed set.
func TestObservationHintInvariants(t *testing.T) {
	visuals := []string{
		"",
		observation.VisualKindLoading,
		"image_heavy(52%)",
		observation.VisualKindMediaSurface,
		observation.VisualKindSparseTree,
	}
	changes := []string{
		"",
		protocol.TreeChangeUnchanged,
		protocol.TreeChangeDetailOnly,
		protocol.TreeChangeMatchedBase,
	}
	for _, visual := range visuals {
		for _, change := range changes {
			for _, hasValidImage := range []bool{false, true} {
				for _, fromWaitFor := range []bool{false, true} {
					hint := observationHint(visual, change, hasValidImage, fromWaitFor)
					name := visual + "/" + change
					if hint != "" && visual == "" && change == "" {
						t.Fatalf("%s: hint %q without a published cue", name, hint)
					}
					if change == protocol.TreeChangeMatchedBase && visual == "" && hint != "" {
						t.Fatalf("%s: matched base without a visual published %q", name, hint)
					}
					if hint == protocol.HintSnap && hasValidImage {
						t.Fatalf("%s: valid image requested another screenshot", name)
					}
					switch hint {
					case "", protocol.HintWaitFor, protocol.HintSnap:
					default:
						t.Fatalf("%s: hint %q is outside the closed set", name, hint)
					}
				}
			}
		}
	}
}
