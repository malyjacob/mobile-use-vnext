package observation

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// leafRowCapture builds one anchored box with identical text leaves at the given
// rectangles. The order of spans is the raw/public sibling order, so reversing
// it simulates two indistinguishable siblings that traded places.
func leafRowCapture(spans []device.Rect) *device.Capture {
	c := newCap(1080, 2400)
	root := c.root()
	box := c.add(root, withClass(visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout"))
	for _, span := range spans {
		c.add(box, withText(withClass(visible(span), "android.widget.TextView"), "重复"))
	}
	return c.capture()
}

func requireStage6(t *testing.T, b *Builder, want int) {
	t.Helper()
	if got := b.lastStats.stage6Matched; got != want {
		t.Fatalf("stage6Matched = %d, want %d (stats=%+v)", got, want, b.lastStats)
	}
}

func leafRefs(snap *Snapshot) []string {
	box := snap.Tree.Root.Children[0]
	refs := make([]string, len(box.Children))
	for i, child := range box.Children {
		refs[i] = child.Ref
	}
	return refs
}

func TestContinuityStaticGeometryLeaves(t *testing.T) {
	t.Run("stationary_separated_identical_leaves_keep_refs", func(t *testing.T) {
		spans := []device.Rect{rect(0, 0, 180, 90), rect(400, 0, 580, 90)}
		b := NewBuilder()
		first := buildOne(t, b, leafRowCapture(spans))
		second := buildOne(t, b, leafRowCapture(spans))
		oldRefs, newRefs := leafRefs(first), leafRefs(second)
		for i := range oldRefs {
			if oldRefs[i] != newRefs[i] {
				t.Fatalf("stationary leaf %d renumbered: %s -> %s\n%s\n%s",
					i, oldRefs[i], newRefs[i], dump(first.Tree), dump(second.Tree))
			}
		}
		requireStage6(t, b, 2)
	})

	t.Run("three_pixel_jitter_keeps_refs", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, leafRowCapture([]device.Rect{
			rect(0, 0, 180, 90), rect(400, 0, 580, 90),
		}))
		second := buildOne(t, b, leafRowCapture([]device.Rect{
			rect(3, 2, 183, 93), rect(397, 1, 577, 91),
		}))
		oldRefs, newRefs := leafRefs(first), leafRefs(second)
		for i := range oldRefs {
			if oldRefs[i] != newRefs[i] {
				t.Fatalf("jittered leaf %d renumbered: %s -> %s\n%s\n%s",
					i, oldRefs[i], newRefs[i], dump(first.Tree), dump(second.Tree))
			}
		}
		requireStage6(t, b, 2)
	})

	t.Run("overlapping_identical_leaves_get_fresh_refs", func(t *testing.T) {
		spans := []device.Rect{rect(0, 0, 180, 90), rect(0, 0, 180, 90)}
		b := NewBuilder()
		first := buildOne(t, b, leafRowCapture(spans))
		oldRefs := map[string]bool{}
		for _, ref := range leafRefs(first) {
			oldRefs[ref] = true
		}
		second := buildOne(t, b, leafRowCapture(spans))
		for i, ref := range leafRefs(second) {
			if oldRefs[ref] {
				t.Fatalf("overlapping leaf %d reused old ref %s\n%s", i, ref, dump(second.Tree))
			}
		}
		requireStage6(t, b, 0)
	})

	t.Run("scrolled_leaves_get_fresh_refs", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, leafRowCapture([]device.Rect{
			rect(0, 0, 180, 90), rect(0, 200, 180, 290),
		}))
		oldRefs := map[string]bool{}
		for _, ref := range leafRefs(first) {
			oldRefs[ref] = true
		}
		second := buildOne(t, b, leafRowCapture([]device.Rect{
			rect(0, 40, 180, 130), rect(0, 240, 180, 330),
		}))
		for i, ref := range leafRefs(second) {
			if oldRefs[ref] {
				t.Fatalf("scrolled leaf %d reused old ref %s\n%s", i, ref, dump(second.Tree))
			}
		}
		requireStage6(t, b, 0)
	})

	t.Run("count_change_never_steals_ref", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, leafRowCapture([]device.Rect{rect(0, 0, 180, 90)}))
		oldRef := leafRefs(first)[0]
		second := buildOne(t, b, leafRowCapture([]device.Rect{
			rect(0, 0, 180, 90), rect(400, 0, 580, 90),
		}))
		for i, ref := range leafRefs(second) {
			if ref == oldRef {
				t.Fatalf("leaf %d reused the single previous ref %s despite a count change\n%s",
					i, ref, dump(second.Tree))
			}
		}
		requireStage6(t, b, 0)
	})

	t.Run("swapped_position_order_rejected", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, leafRowCapture([]device.Rect{
			rect(0, 0, 180, 90), rect(0, 200, 180, 290),
		}))
		oldRefs := map[string]bool{}
		for _, ref := range leafRefs(first) {
			oldRefs[ref] = true
		}
		// The two indistinguishable siblings exchanged their rectangles, so a
		// geometry pairing would have to cross: order-preservation rejects it.
		second := buildOne(t, b, leafRowCapture([]device.Rect{
			rect(0, 200, 180, 290), rect(0, 0, 180, 90),
		}))
		for i, ref := range leafRefs(second) {
			if oldRefs[ref] {
				t.Fatalf("swapped leaf %d reused old ref %s\n%s", i, ref, dump(second.Tree))
			}
		}
		requireStage6(t, b, 0)
	})

	t.Run("unanchored_parent_disables_static_geometry", func(t *testing.T) {
		build := func(resource string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box := c.add(root, clickable(withRes(withClass(
				visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout"), resource)))
			c.add(box, withText(withClass(visible(rect(0, 0, 180, 90)), "android.widget.TextView"), "重复"))
			c.add(box, withText(withClass(visible(rect(400, 0, 580, 90)), "android.widget.TextView"), "重复"))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build("app:id/box_a"))
		oldBox := first.Tree.Root.Children[0]
		oldRefs := map[string]bool{oldBox.Ref: true}
		for _, child := range oldBox.Children {
			oldRefs[child.Ref] = true
		}
		second := buildOne(t, b, build("app:id/box_b"))
		newBox := second.Tree.Root.Children[0]
		if oldRefs[newBox.Ref] {
			t.Fatalf("renamed box kept ref %s\n%s", newBox.Ref, dump(second.Tree))
		}
		for _, child := range newBox.Children {
			if oldRefs[child.Ref] {
				t.Fatalf("leaf under an unanchored parent reused old ref %s\n%s", child.Ref, dump(second.Tree))
			}
		}
		requireStage6(t, b, 0)
	})

	t.Run("editable_leaves_are_never_static_matched", func(t *testing.T) {
		build := func(values [2]string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box := c.add(root, withClass(visible(rect(0, 0, 1080, 500)), "android.widget.LinearLayout"))
			for i, value := range values {
				y := i * 200
				c.add(box, withText(editable(withClass(
					visible(rect(0, y, 900, y+100)), "android.widget.EditText")), value))
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build([2]string{"甲", "乙"}))
		oldRefs := map[string]bool{}
		for _, child := range first.Tree.Root.Children[0].Children {
			oldRefs[child.Ref] = true
		}
		second := buildOne(t, b, build([2]string{"丙", "丁"}))
		for _, child := range second.Tree.Root.Children[0].Children {
			if oldRefs[child.Ref] {
				t.Fatalf("anchorless input reused old ref %s\n%s", child.Ref, dump(second.Tree))
			}
		}
		requireStage6(t, b, 0)
	})
}
