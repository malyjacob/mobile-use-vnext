package observation

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func continuityRawRef(t *testing.T, snap *Snapshot, rawIndex int) string {
	t.Helper()
	for ref, source := range snap.Source {
		if source == rawIndex {
			return ref
		}
	}
	t.Fatalf("raw node %d isn't published:\n%s", rawIndex, dump(snap.Tree))
	return ""
}

func TestContinuityTransparentVIPFrontier(t *testing.T) {
	build := func(directDuplicate bool, wrapperDepth int) (*device.Capture, []int) {
		c := newCap(1080, 2400)
		root := c.root()
		var badges []int
		for i, title := range []string{"Song A", "Song B"} {
			y := 140 + i*200
			span := rect(0, y, 1080, y+150)
			badgeSpan := rect(410, y+20, 470, y+60)
			card := continuityWrapper(c, root, span)
			c.add(card, withText(visible(rect(10, y+20, 390, y+60)), title))
			badgeWrap := continuityWrapper(c, card, span)
			continuityDecoration(c, badgeWrap, rect(480, y+20, 520, y+60))
			for depth := 1; depth < wrapperDepth; depth++ {
				badgeWrap = continuityWrapper(c, badgeWrap, span)
			}
			badges = append(badges, c.add(badgeWrap, withText(withClass(visible(badgeSpan), "android.widget.TextView"), "VIP")))
			// One other same-shape wrapper keeps the VIP wrapper unanchored.
			decorationWrap := continuityWrapper(c, card, span)
			continuityDecoration(c, decorationWrap, rect(540, y+20, 580, y+60))
			if directDuplicate {
				c.add(card, withText(withClass(visible(badgeSpan), "android.widget.TextView"), "VIP"))
			}
		}
		return c.capture(), badges
	}
	for _, tc := range []struct {
		name            string
		directDuplicate bool
		wrapperDepth    int
		wantMatch       bool
	}{
		{"stationary_badges", false, 1, true},
		{"direct_sibling_preserves_ambiguity", true, 1, false},
		{"eight_wrappers_reach_badges", false, 8, true},
		{"nine_wrappers_hide_badges", false, 9, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder()
			oldCap, oldBadges := build(tc.directDuplicate, tc.wrapperDepth)
			curCap, curBadges := build(false, tc.wrapperDepth)
			first := buildOne(t, b, oldCap)
			second := buildOne(t, b, curCap)
			for i, curBadge := range curBadges {
				oldRef := continuityRawRef(t, first, oldBadges[i])
				curRef := continuityRawRef(t, second, curBadge)
				if !tc.wantMatch {
					requireContinuityFresh(t, first, curRef)
					continue
				}
				if oldRef != curRef {
					t.Fatalf("VIP %d changed ref %s -> %s (stats %+v)", i, oldRef, curRef, b.lastStats)
				}
				if tc.wrapperDepth == 1 {
					oldWrapper := continuityRawRef(t, first, oldCap.Nodes[oldBadges[i]].Parent)
					curWrapper := continuityRawRef(t, second, curCap.Nodes[curBadge].Parent)
					if oldWrapper != curWrapper {
						t.Fatalf("matched VIP %d did not anchor its wrapper %s -> %s", i, oldWrapper, curWrapper)
					}
				}
			}
			requireStage6(t, b, 0)
		})
	}
}

func TestContinuityTransparentWrappersRejectRecycledCards(t *testing.T) {
	build := func(titles [2]string, wrapVIP, wrapReplacement bool) *device.Capture {
		c := newCap(1080, 2400)
		scroll := c.add(c.root(), scrollable(withClass(visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
		for i, title := range titles {
			y := 140 + i*200
			span := rect(0, y, 1080, y+150)
			parent := scroll
			if wrapReplacement && title != "Keep" {
				parent = continuityWrapper(c, scroll, span)
			}
			card := continuityWrapper(c, parent, span)
			c.add(card, withText(visible(rect(10, y+20, 390, y+60)), title))
			badgeSpan := rect(410, y+20, 470, y+60)
			badgeParent := card
			if wrapVIP {
				badgeParent = continuityWrapper(c, card, badgeSpan)
			}
			c.add(badgeParent, withText(withClass(visible(badgeSpan), "android.widget.TextView"), "VIP"))
			for _, x := range []int{820, 940} {
				continuityIcon(c, card, rect(x, y+20, x+80, y+100), true)
			}
		}
		return c.capture()
	}
	for _, tc := range []struct {
		name                 string
		oldTitles, curTitles [2]string
		wrapVIP, wrapCard    bool
	}{
		{"scroll_replaces_same_coordinates", [2]string{"Song A", "Song B"}, [2]string{"Song C", "Song D"}, false, false},
		{"scroll_replaces_cards_with_wrapped_vip", [2]string{"Song A", "Song B"}, [2]string{"Song C", "Song D"}, true, true},
		{"local_structure_remaining_one_with_wrapper_rejected", [2]string{"Song A", "Keep"}, [2]string{"Keep", "Song C"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder()
			first := buildOne(t, b, build(tc.oldTitles, tc.wrapVIP, tc.wrapCard))
			second := buildOne(t, b, build(tc.curTitles, tc.wrapVIP, tc.wrapCard))
			oldScroll := first.Tree.Root.Children[0]
			curScroll := second.Tree.Root.Children[0]
			if curScroll.Ref != oldScroll.Ref {
				t.Fatal("fixture must anchor the scroll")
			}
			var checkFresh func(*protocol.Node)
			checkFresh = func(n *protocol.Node) {
				requireContinuityFresh(t, first, n.Ref)
				for _, child := range n.Children {
					checkFresh(child)
				}
			}
			for i, title := range tc.curTitles {
				if title == "Keep" {
					if curScroll.Children[i].Ref != findCardByTitle(first.Tree, "Keep").Ref {
						t.Fatal("fixture must anchor the shifted Keep card")
					}
					continue
				}
				checkFresh(curScroll.Children[i])
			}
		})
	}
}

func TestContinuityTransparentStage6RejectsUnsafeMembers(t *testing.T) {
	for _, tc := range []struct {
		name string
		add  func(*capBuilder, int, device.Rect) int
	}{
		{"anchorless_input", func(c *capBuilder, parent int, span device.Rect) int {
			return c.add(parent, continuityDefaultState(editable(withClass(visible(span), "android.widget.EditText"))))
		}},
		{"anchorless_inputs_with_distinct_classes", func(c *capBuilder, parent int, span device.Rect) int {
			class := "android.widget.EditText"
			if span.Left > 10 {
				class = "androidx.appcompat.widget.AppCompatEditText"
			}
			return c.add(parent, continuityDefaultState(editable(withClass(visible(span), class))))
		}},
		{"non_default_icon_leaf", func(c *capBuilder, parent int, span device.Rect) int {
			icon := continuityIcon(c, parent, span, false)
			c.nodes[icon].Selected = true
			return icon
		}},
		{"non_default_icon_container", func(c *capBuilder, parent int, span device.Rect) int {
			icon := continuityIcon(c, parent, span, true)
			c.nodes[icon].Enabled = false
			return icon
		}},
		{"non_default_decoration", func(c *capBuilder, parent int, span device.Rect) int {
			icon := continuityIcon(c, parent, span, true)
			c.nodes[c.nodes[icon].Children[0]].Focused = true
			return icon
		}},
		{"text_child", func(c *capBuilder, parent int, span device.Rect) int {
			icon := continuityIcon(c, parent, span, true)
			c.add(icon, withText(withClass(visible(span), "android.widget.TextView"), "VIP"))
			return icon
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := func(withStaticSibling bool) (*device.Capture, []int) {
				c := newCap(1080, 2400)
				bar := c.add(c.root(), withText(visible(rect(0, 0, 1080, 100)), "Toolbar"))
				var candidates []int
				for i := 0; i < 2; i++ {
					x := 10 + i*300
					span := rect(x, 10, x+80, 80)
					wrapper := continuityWrapper(c, bar, rect(x, 10, x+180, 80))
					candidates = append(candidates, tc.add(c, wrapper, span))
					if withStaticSibling {
						icon := continuityIcon(c, wrapper, rect(x+90, 10, x+170, 80), true)
						candidates = append(candidates, icon)
					}
				}
				return c.capture(), candidates
			}
			for _, withStaticSibling := range []bool{false, true} {
				b := NewBuilder()
				firstCap, _ := build(withStaticSibling)
				first := buildOne(t, b, firstCap)
				secondCap, candidates := build(withStaticSibling)
				second := buildOne(t, b, secondCap)
				requireStage6(t, b, 0)
				for _, i := range candidates {
					requireContinuityFresh(t, first, continuityRawRef(t, second, i))
				}
			}
		})
	}
}
