package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestSameObservationContext(t *testing.T) {
	present := device.ForegroundPresent
	unknown := device.ForegroundUnknown
	for _, tc := range []struct {
		name string
		a, b device.Foreground
		want bool
	}{
		{"state differs", device.Foreground{State: present}, device.Foreground{State: unknown}, false},
		{"present without task", device.Foreground{State: present, Package: "one"}, device.Foreground{State: present, Package: "one"}, true},
		{"present different packages", device.Foreground{State: present, Package: "one"}, device.Foreground{State: present, Package: "two"}, false},
		{"present missing task", device.Foreground{State: present, TaskID: intPtr(1)}, device.Foreground{State: present}, false},
		{"present equal task values", device.Foreground{State: present, TaskID: intPtr(1)}, device.Foreground{State: present, TaskID: intPtr(1)}, true},
		{"present different tasks", device.Foreground{State: present, TaskID: intPtr(1)}, device.Foreground{State: present, TaskID: intPtr(2)}, false},
		{"unknown default reason", device.Foreground{State: unknown}, device.Foreground{State: unknown, Reason: "foreground_unknown"}, true},
		{"unknown different reason", device.Foreground{State: unknown}, device.Foreground{State: unknown, Reason: "window_missing"}, false},
		{"none has no extra public fields", device.Foreground{State: device.ForegroundNone, Package: "one", TaskID: intPtr(1), Reason: "unused"}, device.Foreground{State: device.ForegroundNone}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if sameForeground(tc.a, tc.b) != tc.want || sameForeground(tc.b, tc.a) != tc.want {
				t.Fatalf("foreground comparison = %v, want %v", sameForeground(tc.a, tc.b), tc.want)
			}
		})
	}

	screen := device.Display{ID: 3, Generation: "first", Width: 1080, Height: 2400, Rotation: 1}
	for _, tc := range []struct {
		name  string
		other device.Display
		want  bool
	}{
		{"identical", screen, true},
		{"different display identity", device.Display{ID: 4, Generation: "second", Width: 1080, Height: 2400, Rotation: 1}, true},
		{"different width", device.Display{Width: 1079, Height: 2400, Rotation: 1}, false},
		{"different height", device.Display{Width: 1080, Height: 2399, Rotation: 1}, false},
		{"different rotation", device.Display{Width: 1080, Height: 2400, Rotation: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameScreenGeometry(screen, tc.other); got != tc.want {
				t.Fatalf("screen geometry comparison = %v, want %v", got, tc.want)
			}
		})
	}
}

func requireObservationCue(t *testing.T, obs *protocol.Observation, visual, change, hint string) {
	t.Helper()
	if obs == nil {
		t.Fatal("no observation returned")
	}
	if obs.Visual != visual || obs.TreeChange != change || obs.Hint != hint {
		t.Fatalf("(visual, tree_change, hint) = (%q, %q, %q), want (%q, %q, %q)",
			obs.Visual, obs.TreeChange, obs.Hint, visual, change, hint)
	}
}

func TestActTreeChangeMatrix(t *testing.T) {
	// A rich page keeps the visual classifiers out of the tree-change assertions.
	// The banner adds exactly one definition and one children declaration to the delta.
	button := rawNodeSpec{
		Parent: 0, Bounds: device.Rect{Left: 40, Top: 1800, Right: 400, Bottom: 1900},
		Class: "android.widget.Button", Text: "Close", Clickable: true,
	}
	pageA := baselinePageStep(append(visualTextPage(), button))
	bannerPage := append(visualTextPage(), button, rawNodeSpec{
		Parent: 0, Bounds: device.Rect{Left: 40, Top: 1600, Right: 1040, Bottom: 1700},
		Class: "android.widget.TextView", Text: "temporary-banner",
	})
	pageB := baselinePageStep(bannerPage)
	// Moving a passive text row changes its full bounds. Compact hides its geometry.
	detailSpecs := append(visualTextPage(), button)
	detailSpecs[1].Bounds.Right--
	pageDetail := baselinePageStep(detailSpecs)

	for _, tc := range []struct {
		name         string
		observeB     bool
		after        captureStep
		changes      int
		change, hint string
	}{
		{"unchanged zero delta", false, pageA, 0, protocol.TreeChangeUnchanged, protocol.HintSnap},
		{"unchanged repeated nonempty delta", true, pageB, 2, protocol.TreeChangeUnchanged, protocol.HintSnap},
		{"detail only", false, pageDetail, 0, protocol.TreeChangeDetailOnly, protocol.HintSnap},
		{"matched old baseline", true, pageA, 0, protocol.TreeChangeMatchedBase, ""},
		{"regular change", false, pageB, 2, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := settleHarness(t)
			h.device.scriptCaptures(pageA)
			first := h.startSession().Observation
			requireFullTree(t, first, 1)
			requireObservationCue(t, first, "", "", "")
			before, revalidate := first, pageA
			if tc.observeB {
				h.device.scriptCaptures(pageB)
				before = baselineObserve(t, h, "PtreeObserveB", first.ID)
				delta := requireBaselineDelta(t, before, 2, first.ID)
				if delta.IsEmpty() {
					t.Fatal("observing page B produced an empty delta")
				}
				requireObservationCue(t, before, "", "", "")
				revalidate = pageB
			}
			h.device.scriptCaptures(revalidate, tc.after)
			target := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
			if target == nil {
				t.Fatal("page has no close button")
			}
			result := h.actWith(t, "PtreeAct", h.tapRefRequest(t, before.ID, target.Ref))
			if result.Outcome != OutcomeExecuted || result.Error != nil {
				t.Fatalf("act = %q, error = %+v", result.Outcome, result.Error)
			}
			delta := requireBaselineDelta(t, result.Observation, before.Revision+1, first.ID)
			changes := len(delta.Add) + len(delta.Remove) + len(delta.Set) + len(delta.Children) + len(delta.Replace)
			if changes != tc.changes {
				t.Fatalf("delta declares %d changes, want %d: %+v", changes, tc.changes, delta)
			}
			requireObservationCue(t, result.Observation, "", tc.change, tc.hint)
		})
	}
}

func TestPublishFullTreeChangeIgnoresRefRenumbering(t *testing.T) {
	for _, renumber := range []bool{false, true} {
		name := "same refs"
		if renumber {
			name = "different refs"
		}
		t.Run(name, func(t *testing.T) {
			h := settleHarness(t)
			h.device.scriptCaptures(baselinePageStep(visualTextPage()))
			h.startSession()
			h.engine.mu.Lock()
			previous := *h.engine.current
			h.engine.mu.Unlock()
			work, session := h.settleWork(t, "PtreeFull")
			tree, apiErr := h.engine.captureTree(work.Ctx, work, session, "act")
			if apiErr != nil {
				t.Fatalf("capture: %v", apiErr)
			}
			if renumber {
				// Relabel only the local copy of the previous frame, in both views.
				// The acquired candidate and retained snapshot stay immutable.
				compact, compactRoot := *previous.Compact, *previous.Compact.Root
				compactRoot.Ref = "n900001"
				compact.Root = &compactRoot
				previous.Compact = &compact
				projection, fullTree := *previous.Projection, *previous.Projection.Tree
				fullRoot := *fullTree.Root
				fullRoot.Ref = compactRoot.Ref
				fullTree.Root = &fullRoot
				projection.Tree = &fullTree
				previous.Projection = &projection
				if tree.compact.Root.Ref == compactRoot.Ref || tree.projection.Tree.Root.Ref == fullRoot.Ref {
					t.Fatal("fixture did not change the previous refs")
				}
			}
			obs, _, apiErr := h.engine.publishTreeAttempt(work, session, "act", tree,
				tree.capture.CapturedAt, treeSelection{forceFull: true}, "complete", nil,
				qualityOf(SettlePolicy{}, nil), &previous)
			if apiErr != nil {
				t.Fatalf("publish: %v", apiErr)
			}
			requireFullTree(t, obs, 2)
			requireObservationCue(t, obs, "", protocol.TreeChangeUnchanged, protocol.HintSnap)
		})
	}
}
