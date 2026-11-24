package observation

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func continuityDefaultState(n device.RawNode) device.RawNode {
	n = focused(selected(enabled(n, true), false), false)
	n.HasPassword, n.HasChecked = true, true
	return n
}

func continuityWrapper(c *capBuilder, parent int, span device.Rect) int {
	return c.add(parent, continuityDefaultState(withClass(visible(span), "android.widget.FrameLayout")))
}

func continuityDecoration(c *capBuilder, parent int, span device.Rect) int {
	return c.add(parent, continuityDefaultState(withRes(withClass(visible(span), "android.widget.ImageView"), "android:id/icon")))
}

func continuityIcon(c *capBuilder, parent int, span device.Rect, hasDecoration bool) int {
	icon := c.add(parent, continuityDefaultState(clickable(withClass(visible(span), "android.view.ViewGroup"))))
	if hasDecoration {
		continuityDecoration(c, icon, span)
	}
	return icon
}

func requireContinuityFresh(t *testing.T, first *Snapshot, ref string) {
	t.Helper()
	if find(first.Tree, ref) != nil {
		t.Fatalf("expected a fresh ref, got %s", ref)
	}
}

func TestContinuityStaticIconContainers(t *testing.T) {
	build := func(spans []device.Rect, wrapped, firstHasDecoration, secondHasDecoration bool) *device.Capture {
		c := newCap(1080, 2400)
		bar := c.add(c.root(), withText(visible(rect(0, 0, 1080, 100)), "Toolbar"))
		parent := bar
		if wrapped {
			// Two anonymous siblings keep Stage 1 from anchoring the icon group.
			parent = continuityWrapper(c, bar, rect(0, 0, 1080, 100))
			other := continuityWrapper(c, bar, rect(0, 0, 1080, 100))
			continuityDecoration(c, other, rect(920, 0, 950, 30))
			for _, x := range []int{960, 1000} {
				continuityDecoration(c, parent, rect(x, 0, x+30, 30))
			}
		}
		for i, span := range spans {
			decorated := firstHasDecoration
			if i%2 == 1 {
				decorated = secondHasDecoration
			}
			continuityIcon(c, parent, span, decorated)
		}
		return c.capture()
	}
	spans := []device.Rect{rect(10, 10, 60, 60), rect(80, 10, 130, 60), rect(150, 10, 200, 60), rect(220, 10, 270, 60)}

	t.Run("stationary_toolbar", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, build(spans, false, true, true))
		second := buildOne(t, b, build(spans, false, true, true))
		requireStage6(t, b, 4)
		for i, icon := range first.Tree.Root.Children[0].Children {
			deco := icon.Children[0]
			if deco.Role != protocol.RoleImage || deco.Resource != nil || first.Capture.Nodes[first.Source[deco.Ref]].ResourceID != "android:id/icon" {
				t.Fatalf("expected an ImageView with a raw ID and no published resource: %+v", deco)
			}
			if got := second.Tree.Root.Children[0].Children[i].Ref; got != icon.Ref {
				t.Fatalf("stationary toolbar icon %d changed ref %s -> %s", i, icon.Ref, got)
			}
		}
		delta, err := ReadDiff(first.Tree.Root.Ref, ProjectCompactTree(first.Tree), ProjectCompactTree(second.Tree))
		if err != nil || !delta.IsEmpty() {
			t.Fatalf("stationary toolbar produced a compact delta: %+v, %v", delta, err)
		}
	})

	t.Run("multi_child_group_skips_decorative_siblings", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, build(spans[:2], true, true, true))
		second := buildOne(t, b, build(spans[:2], true, true, true))
		requireStage6(t, b, 2)
		oldGroup := first.Tree.Root.Children[0].Children[0]
		newGroup := second.Tree.Root.Children[0].Children[0]
		if oldGroup.Ref == newGroup.Ref {
			t.Fatalf("late Stage 6 matches unexpectedly anchored the group %s", newGroup.Ref)
		}
		for i, child := range newGroup.Children {
			if child.Role == protocol.RoleImage {
				requireContinuityFresh(t, first, child.Ref)
			} else if child.Ref != oldGroup.Children[i].Ref {
				t.Fatalf("stationary grouped icon %d changed ref %s -> %s", i, oldGroup.Children[i].Ref, child.Ref)
			}
		}
	})

	for _, depth := range []int{8, 9} {
		name := "eight_wrappers_reach_icons"
		if depth == 9 {
			name = "nine_wrappers_hide_icons"
		}
		t.Run(name, func(t *testing.T) {
			makeFrame := func() (*device.Capture, [2]int) {
				c := newCap(1080, 2400)
				bar := c.add(c.root(), withText(visible(rect(0, 0, 1080, 100)), "Toolbar"))
				parent := bar
				for range depth {
					parent = continuityWrapper(c, parent, rect(0, 0, 1080, 100))
				}
				other := continuityWrapper(c, bar, rect(0, 0, 1080, 100))
				continuityDecoration(c, other, rect(920, 0, 950, 30))
				var icons [2]int
				for i, span := range spans[:2] {
					icons[i] = continuityIcon(c, parent, span, true)
				}
				return c.capture(), icons
			}
			b := NewBuilder()
			oldCap, oldIcons := makeFrame()
			curCap, curIcons := makeFrame()
			first := buildOne(t, b, oldCap)
			second := buildOne(t, b, curCap)
			want := 0
			if depth == 8 {
				want = 2
			}
			requireStage6(t, b, want)
			for i, icon := range curIcons {
				curRef := continuityRawRef(t, second, icon)
				if want == 0 {
					requireContinuityFresh(t, first, curRef)
				} else if oldRef := continuityRawRef(t, first, oldIcons[i]); oldRef != curRef {
					t.Fatalf("icon %d changed ref %s -> %s at depth %d", i, oldRef, curRef, depth)
				}
			}
		})
	}

	t.Run("direct_decorative_image_leaves_keep_existing_semantics", func(t *testing.T) {
		makeFrame := func() *device.Capture {
			c := newCap(1080, 2400)
			bar := c.add(c.root(), withText(visible(rect(0, 0, 1080, 100)), "Toolbar"))
			for _, span := range spans[:2] {
				continuityDecoration(c, bar, span)
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, makeFrame())
		second := buildOne(t, b, makeFrame())
		requireStage6(t, b, 2)
		for i, image := range first.Tree.Root.Children[0].Children {
			if got := second.Tree.Root.Children[0].Children[i].Ref; got != image.Ref {
				t.Fatalf("direct image %d changed ref %s -> %s", i, image.Ref, got)
			}
		}
	})

	for _, tc := range []struct {
		name  string
		cur   []device.Rect
		mixed bool
	}{
		{"swapped", []device.Rect{spans[1], spans[0]}, false},
		{"moved_5px", []device.Rect{rect(15, 10, 65, 60), spans[1]}, false},
		{"mixed_leaf_and_container_swap", spans, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder()
			first := buildOne(t, b, build(spans[:len(tc.cur)], false, true, !tc.mixed))
			second := buildOne(t, b, build(tc.cur, false, !tc.mixed, true))
			requireStage6(t, b, 0)
			for _, icon := range second.Tree.Root.Children[0].Children {
				requireContinuityFresh(t, first, icon.Ref)
			}
		})
	}

	t.Run("budget_exhaustion_discards_the_stage6_batch", func(t *testing.T) {
		b := NewBuilder()
		buildFrame := func() *device.Capture { return build(spans[:2], true, true, true) }
		buildOne(t, b, buildFrame())
		buildOne(t, b, buildFrame())
		requireStage6(t, b, 2)

		limited := NewBuilder()
		first := buildOne(t, limited, buildFrame())
		limited.hasBudgetLimit = true
		limited.budgetLimit = b.lastStats.workUsed - 1
		second := buildOne(t, limited, buildFrame())
		if !limited.lastStats.budgetHit || limited.lastStats.stoppedStage != "static_geometry" {
			t.Fatalf("expected exhaustion inside Stage 6, got %+v", limited.lastStats)
		}
		requireStage6(t, limited, 0)
		for _, icon := range second.Tree.Root.Children[0].Children[0].Children[2:] {
			requireContinuityFresh(t, first, icon.Ref)
		}
		assertUniqueTreeRefs(t, second)
	})
}
