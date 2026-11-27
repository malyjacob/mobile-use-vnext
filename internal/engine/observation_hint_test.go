package engine

import (
	"context"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/imaging"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestObservationHint(t *testing.T) {
	for _, tc := range []struct {
		name, visual, change string
		validImage           bool
		fromWaitFor          bool
		want                 string
	}{
		{"loading without image", observation.VisualKindLoading, "", false, false, protocol.HintWaitFor},
		{"loading with image and unchanged tree", observation.VisualKindLoading, protocol.TreeChangeUnchanged, true, false, protocol.HintWaitFor},
		{"loading on a wait terminal frame", observation.VisualKindLoading, "", false, true, protocol.HintWaitFor},
		{"unchanged without image", "", protocol.TreeChangeUnchanged, false, false, protocol.HintSnap},
		{"detail only without image", "", protocol.TreeChangeDetailOnly, false, false, protocol.HintSnap},
		{"unchanged with image", "", protocol.TreeChangeUnchanged, true, false, ""},
		{"matched base alone", "", protocol.TreeChangeMatchedBase, false, false, ""},
		{"matched base with visual", observation.VisualKindMediaSurface, protocol.TreeChangeMatchedBase, false, false, protocol.HintSnap},
		{"media surface without image", observation.VisualKindMediaSurface, "", false, false, protocol.HintSnap},
		{"media surface with image", observation.VisualKindMediaSurface, "", true, false, ""},
		{"sparse with image", observation.VisualKindSparseTree, "", true, false, protocol.HintWaitFor},
		{"sparse without image", observation.VisualKindSparseTree, "", false, false, protocol.HintWaitFor},
		{"sparse after a matched base", observation.VisualKindSparseTree, protocol.TreeChangeMatchedBase, false, false, protocol.HintWaitFor},
		{"sparse after a matched base with image", observation.VisualKindSparseTree, protocol.TreeChangeMatchedBase, true, false, protocol.HintWaitFor},
		{"sparse unchanged without image", observation.VisualKindSparseTree, protocol.TreeChangeUnchanged, false, false, protocol.HintSnap},
		{"sparse unchanged with image", observation.VisualKindSparseTree, protocol.TreeChangeUnchanged, true, false, ""},
		{"sparse detail only without image", observation.VisualKindSparseTree, protocol.TreeChangeDetailOnly, false, false, protocol.HintSnap},
		{"sparse detail only with image", observation.VisualKindSparseTree, protocol.TreeChangeDetailOnly, true, false, ""},
		{"sparse wait terminal frame without image", observation.VisualKindSparseTree, "", false, true, protocol.HintSnap},
		{"sparse wait terminal frame with image", observation.VisualKindSparseTree, "", true, true, ""},
		{"sparse wait terminal unchanged without image", observation.VisualKindSparseTree, protocol.TreeChangeUnchanged, false, true, protocol.HintSnap},
		{"sparse wait terminal matched base without image", observation.VisualKindSparseTree, protocol.TreeChangeMatchedBase, false, true, protocol.HintSnap},
		{"no cues", "", "", false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := observationHint(tc.visual, tc.change, tc.validImage, tc.fromWaitFor); got != tc.want {
				t.Fatalf("hint = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestActUnchangedTreeImageHint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fail     bool
		wantHint string
	}{
		{"attached image suppresses snap", false, ""},
		{"independent image failure keeps snap", true, protocol.HintSnap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, encoder := imageTestHarness(t)
			start := h.startSession().Observation
			setFakeImageSource(h, time.Now())
			if tc.fail {
				encoder.failWith(imaging.ErrTooLarge)
			}
			result := h.actWith(t, "PtreeImage", tapButtonRequest(t, h, start.ID))
			if result.Outcome != OutcomeExecuted || result.Observation == nil || result.Observation.ID == "" {
				t.Fatalf("act returned no executed post observation: %+v", result)
			}
			obs := result.Observation
			if tc.fail {
				assertImageSlotFailure(t, obs, protocol.CodeImageTooLarge)
			} else if obs.Status != "complete" || obs.Image == nil || obs.Image.Error != nil || obs.Image.Base64 == "" {
				t.Fatalf("no attached image in complete observation: %+v", obs)
			}
			requireObservationCue(t, obs, "", protocol.TreeChangeUnchanged, tc.wantHint)
		})
	}
}

// withObservationProgress adds a visible raw loading indicator away from the
// action targets. It exercises the classifier on the frame delivered by the engine.
func withObservationProgress(step captureStep) captureStep {
	return func(f *fakeDevice, display device.Display) (device.Capture, error) {
		capture, err := step(f, display)
		if err != nil {
			return capture, err
		}
		bounds := device.Rect{Left: 480, Top: 900, Right: 600, Bottom: 1020}
		index := len(capture.Nodes)
		capture.Nodes = append(capture.Nodes, device.RawNode{
			Index: index, Parent: capture.Root, Depth: 1, DisplayID: display.ID,
			ClassName: "android.widget.ProgressBar", Package: capture.Foreground.Package,
			RawBounds: bounds, Bounds: bounds, Visible: true, HasEnabled: true, Enabled: true,
		})
		root := &capture.Nodes[capture.Root]
		root.Children = append(root.Children, index)
		return capture, nil
	}
}

func TestObserveLoadingDeltaKeepsWaitForHint(t *testing.T) {
	h := settleHarness(t)
	h.device.scriptCaptures(withObservationProgress(defaultCaptureStep))
	first := h.startSession().Observation
	requireObservationCue(t, first, observation.VisualKindLoading, "", protocol.HintWaitFor)
	next := baselineObserve(t, h, "PloadingObserve", first.ID)
	if delta := requireBaselineDelta(t, next, 2, first.ID); !delta.IsEmpty() {
		t.Fatalf("unchanged loading frame has nonempty delta: %+v", delta)
	}
	requireObservationCue(t, next, observation.VisualKindLoading, "", protocol.HintWaitFor)
}

func TestActRejectionDeliveryKeepsVisualHintAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name         string
		step         captureStep
		visual, hint string
	}{
		// A same-band vertical move is now re-acquired by Tier 5, so both
		// rejection deliveries use a target text change instead. A delivered
		// full frame carries no tree change, so a sparse page is routed to
		// wait_for: the transition frame may still be loading its content.
		{"sparse full delivery", markerCapture("OK-changed"), observation.VisualKindSparseTree, protocol.HintWaitFor},
		{"loading full delivery", withObservationProgress(markerCapture("OK-changed")), observation.VisualKindLoading, protocol.HintWaitFor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := settleHarness(t)
			first := h.startSession().Observation
			// Rejection of a delta reference delivers a new full frame.
			before := baselineObserve(t, h, "PdeliveryBefore", first.ID)
			requireBaselineDelta(t, before, 2, first.ID)
			h.device.scriptCaptures(tc.step)
			button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
			if button == nil {
				t.Fatal("button not found")
			}
			result := h.actWith(t, "PdeliveryVisual", h.tapRefRequest(t, before.ID, button.Ref))
			requireNotExecuted(t, result, protocol.CodeTargetChanged)
			requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
				protocol.RecoveryInputEffective, "")
			requireFullTree(t, result.Observation, 3)
			if result.Observation.ID != h.currentObservationID() || observationStatus(h, result.Observation.ID) != ObservationReady {
				t.Fatalf("delivered observation %q is not ready", result.Observation.ID)
			}
			requireObservationCue(t, result.Observation, tc.visual, "", tc.hint)
		})
	}
}

func TestActTextPartialDeliveryKeepsVisualHint(t *testing.T) {
	bounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
	h, dev, reference, ref := newTextFlowHarness(t, bounds)
	// A zero-candidate submit read forces a post-write rebind capture. The
	// rebound read also finds zero candidates, so delivery must reuse that frame.
	dev.getTextFunc = func(context.Context, device.TextTarget) (device.TextResult, error) {
		return device.TextResult{CandidateCount: 0}, nil
	}
	h.device.scriptCaptures(
		textFlowBuild(0, bounds, "", false),
		textFlowBuild(0, bounds, "", true),
		withObservationProgress(textFlowBuild(0, bounds, "hello", true)),
		failingCapture(),
	)
	result := h.actWith(t, "PtextPartialVisual", textFlowRequest(reference, ref, "hello", nil))
	requirePartialTextResult(t, result, protocol.CodeTextSubmitNotExecuted, "submit", "submit_target_unconfirmed")
	requireObservationCue(t, result.Observation, observation.VisualKindLoading, "", protocol.HintWaitFor)
	if result.Observation.ID == "" || observationStatus(h, result.Observation.ID) != ObservationReady || result.Recovery != nil {
		t.Fatalf("partial text did not deliver a ready observation: %+v", result)
	}
	requireQuality(t, result.Observation.Quality, protocol.QualityUnknown, 0, protocol.QualityStopNotSampled)
}

func TestActStaticMissingRefCarriesNoObservationHint(t *testing.T) {
	h := settleHarness(t)
	before := h.startSession().Observation
	result := h.actWith(t, "PstaticMissingRef", h.tapRefRequest(t, before.ID, "n900001"))
	requireNotExecuted(t, result, protocol.CodeNodeNotFound)
	if result.Observation != nil || observationStatus(h, before.ID) != ObservationReady {
		t.Fatalf("static rejection changed the observation: %+v", result)
	}
}
