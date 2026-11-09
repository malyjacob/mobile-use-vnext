package observation

import (
	"reflect"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// previewPage builds a page with one or two labelled nodes. The second node only
// exists in the extended page, so building it allocates a fresh ref.
func previewPage(extended bool) *device.Capture {
	build := newCap(1080, 2400)
	build.root()
	build.add(0, withText(visible(device.Rect{Left: 10, Top: 10, Right: 110, Bottom: 60}), "One"))
	if extended {
		build.add(0, withText(visible(device.Rect{Left: 10, Top: 70, Right: 110, Bottom: 120}), "Two"))
	}
	return build.capture()
}

// TestPreviewKeepsContinuityState proves the settle sampling guard: a preview
// must leave both the previous snapshot and the ref counter untouched, so the
// next real build numbers refs exactly as it would without the preview.
func TestPreviewKeepsContinuityState(t *testing.T) {
	control := NewBuilder()
	if _, err := control.Build(previewPage(false)); err != nil {
		t.Fatalf("control build failed: %v", err)
	}
	want, err := control.Build(previewPage(true))
	if err != nil {
		t.Fatalf("control build failed: %v", err)
	}

	sampled := NewBuilder()
	first, err := sampled.Build(previewPage(false))
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if previewed, err := sampled.Preview(previewPage(true)); err != nil || previewed == nil {
		t.Fatalf("preview failed: %v", err)
	}
	if sampled.Previous() != first {
		t.Fatal("preview advanced the previous snapshot")
	}
	got, err := sampled.Build(previewPage(true))
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	if !reflect.DeepEqual(got.Source, want.Source) {
		t.Fatalf("refs drifted after a preview:\n got %v\nwant %v", got.Source, want.Source)
	}
	if !reflect.DeepEqual(got.Refs, want.Refs) {
		t.Fatalf("ref order drifted after a preview:\n got %v\nwant %v", got.Refs, want.Refs)
	}
}
