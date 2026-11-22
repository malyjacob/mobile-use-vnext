package observation

import (
	"fmt"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func scrollCardsCapture(titles []string, titleOffset int, favPerCard int) *device.Capture {
	c := newCap(1080, 2400)
	root := c.root()
	scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
	for idx, title := range titles {
		y := 120 + idx*380
		card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+340)), "android.widget.LinearLayout"))
		c.add(card, withText(withRes(withClass(visible(rect(20, y+20, 800, y+120)), "android.widget.TextView"), "app:id/tv_title"), title))
		for b := 0; b < favPerCard; b++ {
			bx := 820 + b*110
			btn := clickable(withRes(withClass(withText(visible(rect(bx, y+20, bx+100, y+120)), "收藏"), "android.widget.Button"), "app:id/fav"))
			c.add(card, btn)
		}
	}
	_ = titleOffset
	return c.capture()
}

func findCardByTitle(tree *protocol.Tree, title string) *protocol.Node {
	var walk func(n *protocol.Node) *protocol.Node
	walk = func(n *protocol.Node) *protocol.Node {
		if n == nil {
			return nil
		}
		for _, child := range n.Children {
			if len(child.Label) > 0 && child.Label[0] == title {
				return n
			}
			if found := walk(child); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(tree.Root)
}

func assertUniqueTreeRefs(t *testing.T, snap *Snapshot) {
	t.Helper()
	seen := make(map[string]bool, len(snap.Refs))
	for _, ref := range snap.Refs {
		if ref == "" {
			t.Fatalf("empty ref in snapshot: %v", snap.Refs)
		}
		if seen[ref] {
			t.Fatalf("duplicate ref %q in snapshot: %v", ref, snap.Refs)
		}
		seen[ref] = true
	}
}

func TestContinuityAnchorlessInputOnlyMatchesSameParent(t *testing.T) {
	t.Run("same_parent_single_anchorless_input_keeps_ref", func(t *testing.T) {
		build := func(val string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box := c.add(root, withRes(withClass(visible(rect(0, 0, 1080, 300)), "android.widget.LinearLayout"), "app:id/form"))
			c.add(box, withText(editable(withClass(visible(rect(10, 10, 1000, 100)), "android.widget.EditText")), val))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build(""))
		second := buildOne(t, b, build("hello"))
		if first.Tree.Root.Children[0].Children[0].Ref != second.Tree.Root.Children[0].Children[0].Ref {
			t.Fatalf("single anchorless input under anchored parent changed ref: %s -> %s",
				first.Tree.Root.Children[0].Children[0].Ref, second.Tree.Root.Children[0].Children[0].Ref)
		}
	})

	t.Run("same_parent_two_anchorless_inputs_both_get_fresh_refs", func(t *testing.T) {
		build := func(v1, v2 string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box := c.add(root, withRes(withClass(visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout"), "app:id/form"))
			c.add(box, withText(editable(withClass(visible(rect(10, 10, 1000, 100)), "android.widget.EditText")), v1))
			c.add(box, withText(editable(withClass(visible(rect(10, 120, 1000, 210)), "android.widget.EditText")), v2))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build("a", "b"))
		oldRefs := map[string]bool{
			first.Tree.Root.Children[0].Children[0].Ref: true,
			first.Tree.Root.Children[0].Children[1].Ref: true,
		}
		second := buildOne(t, b, build("c", "d"))
		for _, child := range second.Tree.Root.Children[0].Children {
			if oldRefs[child.Ref] {
				t.Fatalf("ambiguous anchorless input reused ref %s", child.Ref)
			}
		}
	})

	t.Run("cross_parent_unique_anchorless_input_gets_fresh_ref", func(t *testing.T) {
		build := func(inSecond bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			boxA := c.add(root, withRes(clickable(withClass(visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout")), "app:id/box_a"))
			c.add(boxA, withText(withClass(visible(rect(10, 10, 300, 80)), "android.widget.TextView"), "A"))
			boxB := c.add(root, withRes(clickable(withClass(visible(rect(0, 420, 1080, 820)), "android.widget.LinearLayout")), "app:id/box_b"))
			c.add(boxB, withText(withClass(visible(rect(10, 430, 300, 500)), "android.widget.TextView"), "B"))
			target := boxA
			if inSecond {
				target = boxB
			}
			c.add(target, withText(editable(withClass(visible(rect(10, 100, 900, 200)), "android.widget.EditText")), "typed"))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build(false))
		oldInputRef := first.Tree.Root.Children[0].Children[1].Ref
		second := buildOne(t, b, build(true))
		newInputRef := second.Tree.Root.Children[1].Children[1].Ref
		if newInputRef == oldInputRef {
			t.Fatalf("cross-parent anchorless input reused old ref %s", oldInputRef)
		}
	})

	t.Run("anchorless_input_action_change_breaks_identity", func(t *testing.T) {
		build := func(canTap bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			in := withText(editable(withClass(visible(rect(10, 10, 900, 100)), "android.widget.EditText")), "x")
			if canTap {
				in = clickable(in)
			}
			c.add(root, in)
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build(false))
		second := buildOne(t, b, build(true))
		if first.Tree.Root.Children[0].Ref == second.Tree.Root.Children[0].Ref {
			t.Fatalf("anchorless input kept ref %s across action mask change", first.Tree.Root.Children[0].Ref)
		}
	})
}

func TestContinuityBatchUniqueIndependentOfTraversal(t *testing.T) {
	buildPermutedRaw := func(reverseRawStorage bool) *device.Capture {
		c := newCap(1080, 2400)
		root := c.root()
		scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
		for i := 0; i < 3; i++ {
			y := 120 + i*300
			card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+250)), "android.widget.LinearLayout"))
			c.add(card, withText(withRes(withClass(visible(rect(10, y+10, 500, y+90)), "android.widget.TextView"), "app:id/title"), fmt.Sprintf("item-%d", i)))
			c.add(card, clickable(withRes(withClass(withText(visible(rect(600, y+10, 800, y+90)), "赞"), "android.widget.Button"), "app:id/like")))
		}
		dupBar := c.add(root, withRes(clickable(withClass(visible(rect(0, 2220, 1080, 2380)), "android.widget.LinearLayout")), "app:id/dup_bar"))
		c.add(dupBar, clickable(withRes(withClass(withText(visible(rect(10, 2230, 200, 2370)), "重复"), "android.widget.Button"), "app:id/dup")))
		c.add(dupBar, clickable(withRes(withClass(withText(visible(rect(220, 2230, 410, 2370)), "重复"), "android.widget.Button"), "app:id/dup")))

		raw := c.capture()
		if !reverseRawStorage {
			return raw
		}
		// Reverse non-root indices in Nodes slice while preserving Parent/Children topology.
		n := len(raw.Nodes)
		remap := make([]int, n)
		remap[0] = 0
		for i := 1; i < n; i++ {
			remap[i] = n - i
		}
		reordered := make([]device.RawNode, n)
		for oldIdx, node := range raw.Nodes {
			newIdx := remap[oldIdx]
			cp := node
			cp.Index = newIdx
			if cp.Parent >= 0 {
				cp.Parent = remap[cp.Parent]
			}
			if len(node.Children) > 0 {
				cp.Children = make([]int, len(node.Children))
				for j, ch := range node.Children {
					cp.Children[j] = remap[ch]
				}
			}
			reordered[newIdx] = cp
		}
		raw.Nodes = reordered
		return raw
	}

	b1 := NewBuilder()
	_ = buildOne(t, b1, buildPermutedRaw(false))
	s1 := buildOne(t, b1, buildPermutedRaw(false))

	b2 := NewBuilder()
	_ = buildOne(t, b2, buildPermutedRaw(false))
	s2 := buildOne(t, b2, buildPermutedRaw(true))

	if len(s1.Refs) != len(s2.Refs) {
		t.Fatalf("ref count mismatch: %d vs %d", len(s1.Refs), len(s2.Refs))
	}
	for i := range s1.Refs {
		if s1.Refs[i] != s2.Refs[i] {
			t.Fatalf("traversal order affected ref at %d: %s vs %s", i, s1.Refs[i], s2.Refs[i])
		}
	}

	// Reordering siblings must keep matched refs identical and never let earlier ambiguous items steal.
	buildSiblings := func(reverseOrder bool) *device.Capture {
		c := newCap(1080, 2400)
		root := c.root()
		items := []struct {
			res   string
			label string
		}{
			{"app:id/dup", "同款"},
			{"app:id/dup", "同款"},
			{"app:id/u1", "唯一甲"},
			{"app:id/u2", "唯一乙"},
		}
		if reverseOrder {
			items = []struct {
				res   string
				label string
			}{
				{"app:id/u2", "唯一乙"},
				{"app:id/dup", "同款"},
				{"app:id/u1", "唯一甲"},
				{"app:id/dup", "同款"},
			}
		}
		for i, item := range items {
			c.add(root, clickable(withRes(withText(withClass(visible(rect(0, i*100, 400, i*100+80)), "android.widget.Button"), item.label), item.res)))
		}
		return c.capture()
	}
	b3 := NewBuilder()
	base := buildOne(t, b3, buildSiblings(false))
	u1Old := refOf(base, "唯一甲")
	u2Old := refOf(base, "唯一乙")
	dupOlds := map[string]bool{}
	for _, ch := range base.Tree.Root.Children {
		if ch.Label[0] == "同款" {
			dupOlds[ch.Ref] = true
		}
	}
	rev := buildOne(t, b3, buildSiblings(true))
	if refOf(rev, "唯一甲") != u1Old || refOf(rev, "唯一乙") != u2Old {
		t.Fatalf("unique siblings drifted on reorder: (%s,%s) -> (%s,%s)", u1Old, u2Old, refOf(rev, "唯一甲"), refOf(rev, "唯一乙"))
	}
	for _, ch := range rev.Tree.Root.Children {
		if ch.Label[0] == "同款" && dupOlds[ch.Ref] {
			t.Fatalf("ambiguous sibling stole old ref %s on reorder", ch.Ref)
		}
	}
}

func TestContinuityDescendantVoteRequiresEqualPairSets(t *testing.T) {
	t.Run("missing_descendant_pair_rejects_container_match", func(t *testing.T) {
		oldCap := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			card := c.add(root, withClass(visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout"))
			c.add(card, withText(withRes(withClass(visible(rect(10, 10, 400, 80)), "android.widget.TextView"), "app:id/a"), "A"))
			c.add(card, withText(withRes(withClass(visible(rect(10, 100, 400, 180)), "android.widget.TextView"), "app:id/b"), "B"))
			other := c.add(root, withClass(visible(rect(0, 420, 1080, 700)), "android.widget.LinearLayout"))
			c.add(other, withText(withClass(visible(rect(10, 430, 400, 500)), "android.widget.TextView"), "OldOther"))
			return c.capture()
		}
		newCapMovedOut := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			card := c.add(root, withClass(visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout"))
			c.add(card, withText(withRes(withClass(visible(rect(10, 10, 400, 80)), "android.widget.TextView"), "app:id/a"), "A"))
			// B escaped outside card directly under root
			c.add(root, withText(withRes(withClass(visible(rect(10, 100, 400, 180)), "android.widget.TextView"), "app:id/b"), "B"))
			other := c.add(root, withClass(visible(rect(0, 420, 1080, 700)), "android.widget.LinearLayout"))
			c.add(other, withText(withClass(visible(rect(10, 430, 400, 500)), "android.widget.TextView"), "NewOther"))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, oldCap())
		oldCardRef := findCardByTitle(first.Tree, "A").Ref
		second := buildOne(t, b, newCapMovedOut())
		newCardRef := findCardByTitle(second.Tree, "A").Ref
		if newCardRef == oldCardRef {
			t.Fatalf("card matched despite descendant B moving outside card (Rc != Rp): %s", oldCardRef)
		}
		if refOf(second, "A") != refOf(first, "A") || refOf(second, "B") != refOf(first, "B") {
			t.Fatalf("leaf content refs should still be preserved")
		}
	})

	t.Run("extra_descendant_pair_rejects_container_match", func(t *testing.T) {
		oldCap := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			card := c.add(root, withClass(visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout"))
			c.add(card, withText(withRes(withClass(visible(rect(10, 10, 400, 80)), "android.widget.TextView"), "app:id/a"), "A"))
			c.add(root, withText(withRes(withClass(visible(rect(10, 100, 400, 180)), "android.widget.TextView"), "app:id/b"), "B"))
			other := c.add(root, withClass(visible(rect(0, 420, 1080, 700)), "android.widget.LinearLayout"))
			c.add(other, withText(withClass(visible(rect(10, 430, 400, 500)), "android.widget.TextView"), "OldOther"))
			return c.capture()
		}
		newCapMovedIn := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			card := c.add(root, withClass(visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout"))
			c.add(card, withText(withRes(withClass(visible(rect(10, 10, 400, 80)), "android.widget.TextView"), "app:id/a"), "A"))
			c.add(card, withText(withRes(withClass(visible(rect(10, 100, 400, 180)), "android.widget.TextView"), "app:id/b"), "B"))
			other := c.add(root, withClass(visible(rect(0, 420, 1080, 700)), "android.widget.LinearLayout"))
			c.add(other, withText(withClass(visible(rect(10, 430, 400, 500)), "android.widget.TextView"), "NewOther"))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, oldCap())
		oldCardRef := findCardByTitle(first.Tree, "A").Ref
		second := buildOne(t, b, newCapMovedIn())
		newCardRef := findCardByTitle(second.Tree, "A").Ref
		if newCardRef == oldCardRef {
			t.Fatalf("card matched despite extra descendant B entering card (Rc != Rp): %s", oldCardRef)
		}
	})

	t.Run("empty_descendant_pair_set_never_votes", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, scrollCardsCapture([]string{"Keep", "OldTail"}, 0, 1))
		second := buildOne(t, b, scrollCardsCapture([]string{"NewHead", "Keep"}, 0, 1))

		oldKeep := findCardByTitle(first.Tree, "Keep")
		newKeep := findCardByTitle(second.Tree, "Keep")
		if oldKeep.Ref != newKeep.Ref {
			t.Fatalf("shifted fixed card Keep lost ref: %s -> %s", oldKeep.Ref, newKeep.Ref)
		}
		oldTail := findCardByTitle(first.Tree, "OldTail")
		newHead := findCardByTitle(second.Tree, "NewHead")
		if newHead.Ref == oldTail.Ref {
			t.Fatalf("empty descendant sets (Rc == Rp == empty) falsely voted to reuse ref %s", oldTail.Ref)
		}
	})
}

func TestContinuityDescendantVoteRejectsSplitMergeAndCrossAnchor(t *testing.T) {
	t.Run("split_container_rejected", func(t *testing.T) {
		oldCap := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			p1 := c.add(root, withClass(visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout"))
			c.add(p1, withText(withClass(visible(rect(10, 10, 400, 80)), "android.widget.TextView"), "A"))
			c.add(p1, withText(withClass(visible(rect(10, 100, 400, 180)), "android.widget.TextView"), "B"))
			p2 := c.add(root, withClass(visible(rect(0, 420, 1080, 700)), "android.widget.LinearLayout"))
			c.add(p2, withText(withClass(visible(rect(10, 430, 400, 500)), "android.widget.TextView"), "OldS"))
			return c.capture()
		}
		newCapSplit := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			// Same K=2 shape sibling count, but p1 was split into c1 and c2 while p2 disappeared.
			c1 := c.add(root, withClass(visible(rect(0, 0, 1080, 200)), "android.widget.LinearLayout"))
			c.add(c1, withText(withClass(visible(rect(10, 10, 400, 80)), "android.widget.TextView"), "A"))
			c2 := c.add(root, withClass(visible(rect(0, 220, 1080, 420)), "android.widget.LinearLayout"))
			c.add(c2, withText(withClass(visible(rect(10, 230, 400, 300)), "android.widget.TextView"), "B"))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, oldCap())
		oldP1 := findCardByTitle(first.Tree, "A").Ref
		oldP2 := findCardByTitle(first.Tree, "OldS").Ref
		second := buildOne(t, b, newCapSplit())
		c1Ref := findCardByTitle(second.Tree, "A").Ref
		c2Ref := findCardByTitle(second.Tree, "B").Ref
		if c1Ref == oldP1 || c1Ref == oldP2 || c2Ref == oldP1 || c2Ref == oldP2 {
			t.Fatalf("split containers reused old refs: old=(%s,%s) new=(%s,%s)", oldP1, oldP2, c1Ref, c2Ref)
		}
	})

	t.Run("merge_containers_rejected", func(t *testing.T) {
		oldCap := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			p1 := c.add(root, withClass(visible(rect(0, 0, 1080, 200)), "android.widget.LinearLayout"))
			c.add(p1, withText(withClass(visible(rect(10, 10, 400, 80)), "android.widget.TextView"), "A"))
			p2 := c.add(root, withClass(visible(rect(0, 220, 1080, 420)), "android.widget.LinearLayout"))
			c.add(p2, withText(withClass(visible(rect(10, 230, 400, 300)), "android.widget.TextView"), "B"))
			return c.capture()
		}
		newCapMerged := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			merged := c.add(root, withClass(visible(rect(0, 0, 1080, 400)), "android.widget.LinearLayout"))
			c.add(merged, withText(withClass(visible(rect(10, 10, 400, 80)), "android.widget.TextView"), "A"))
			c.add(merged, withText(withClass(visible(rect(10, 100, 400, 180)), "android.widget.TextView"), "B"))
			extra := c.add(root, withClass(visible(rect(0, 420, 1080, 700)), "android.widget.LinearLayout"))
			c.add(extra, withText(withClass(visible(rect(10, 430, 400, 500)), "android.widget.TextView"), "NewExtra"))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, oldCap())
		oldP1 := findCardByTitle(first.Tree, "A").Ref
		oldP2 := findCardByTitle(first.Tree, "B").Ref
		second := buildOne(t, b, newCapMerged())
		mergedRef := findCardByTitle(second.Tree, "A").Ref
		if mergedRef == oldP1 || mergedRef == oldP2 {
			t.Fatalf("merged container reused old ref %s (old=%s,%s)", mergedRef, oldP1, oldP2)
		}
	})

	t.Run("cross_anchor_descendant_rejected", func(t *testing.T) {
		oldCap := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box1 := c.add(root, withRes(clickable(withClass(visible(rect(0, 0, 1080, 800)), "android.widget.LinearLayout")), "app:id/box1"))
			c1 := c.add(box1, withClass(visible(rect(10, 10, 1000, 300)), "android.widget.FrameLayout"))
			c.add(c1, withText(withClass(visible(rect(20, 20, 400, 90)), "android.widget.TextView"), "Old1"))
			c1b := c.add(box1, withClass(visible(rect(10, 320, 1000, 600)), "android.widget.FrameLayout"))
			c.add(c1b, withText(withClass(visible(rect(20, 330, 400, 400)), "android.widget.TextView"), "Old1B"))

			box2 := c.add(root, withRes(clickable(withClass(visible(rect(0, 820, 1080, 1600)), "android.widget.LinearLayout")), "app:id/box2"))
			c2 := c.add(box2, withClass(visible(rect(10, 830, 1000, 1100)), "android.widget.FrameLayout"))
			c.add(c2, withText(withClass(visible(rect(20, 840, 400, 910)), "android.widget.TextView"), "CrossLeaf"))
			return c.capture()
		}
		newCapCross := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box1 := c.add(root, withRes(clickable(withClass(visible(rect(0, 0, 1080, 800)), "android.widget.LinearLayout")), "app:id/box1"))
			c1 := c.add(box1, withClass(visible(rect(10, 10, 1000, 300)), "android.widget.FrameLayout"))
			c.add(c1, withText(withClass(visible(rect(20, 20, 400, 90)), "android.widget.TextView"), "CrossLeaf"))
			c1b := c.add(box1, withClass(visible(rect(10, 320, 1000, 600)), "android.widget.FrameLayout"))
			c.add(c1b, withText(withClass(visible(rect(20, 330, 400, 400)), "android.widget.TextView"), "New1B"))

			box2 := c.add(root, withRes(clickable(withClass(visible(rect(0, 820, 1080, 1600)), "android.widget.LinearLayout")), "app:id/box2"))
			c2 := c.add(box2, withClass(visible(rect(10, 830, 1000, 1100)), "android.widget.FrameLayout"))
			c.add(c2, withText(withClass(visible(rect(20, 840, 400, 910)), "android.widget.TextView"), "New2"))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, oldCap())
		oldC1 := findCardByTitle(first.Tree, "Old1").Ref
		oldC2 := findCardByTitle(first.Tree, "CrossLeaf").Ref
		second := buildOne(t, b, newCapCross())
		newC1 := findCardByTitle(second.Tree, "CrossLeaf").Ref
		if newC1 == oldC1 || newC1 == oldC2 {
			t.Fatalf("cross-anchor container reused ref %s (oldC1=%s, oldC2=%s)", newC1, oldC1, oldC2)
		}
	})
}

func TestContinuityScrollListShiftsAndAnchoredParentLocalRecovery(t *testing.T) {
	t.Run("five_card_scroll_preserves_B_to_E_and_allocates_fresh_for_F", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, scrollCardsCapture([]string{"A", "B", "C", "D", "E"}, 0, 1))
		second := buildOne(t, b, scrollCardsCapture([]string{"B", "C", "D", "E", "F"}, 1, 1))
		assertUniqueTreeRefs(t, second)

		firstUsed := map[string]bool{}
		for _, ref := range first.Refs {
			firstUsed[ref] = true
		}
		cardA := findCardByTitle(first.Tree, "A")
		if cardA == nil {
			t.Fatal("missing card A in first frame")
		}
		for _, title := range []string{"B", "C", "D", "E"} {
			oldCard := findCardByTitle(first.Tree, title)
			newCard := findCardByTitle(second.Tree, title)
			if oldCard == nil || newCard == nil {
				t.Fatalf("missing card %s", title)
			}
			if oldCard.Ref != newCard.Ref {
				t.Fatalf("surviving card %s container renumbered: %s -> %s", title, oldCard.Ref, newCard.Ref)
			}
			if oldCard.Children[0].Ref != newCard.Children[0].Ref {
				t.Fatalf("surviving card %s title renumbered: %s -> %s", title, oldCard.Children[0].Ref, newCard.Children[0].Ref)
			}
			if oldCard.Children[1].Ref != newCard.Children[1].Ref {
				t.Fatalf("surviving card %s fav button renumbered: %s -> %s", title, oldCard.Children[1].Ref, newCard.Children[1].Ref)
			}
		}
		cardF := findCardByTitle(second.Tree, "F")
		if cardF == nil {
			t.Fatal("missing card F in second frame")
		}
		if firstUsed[cardF.Ref] || firstUsed[cardF.Children[0].Ref] || firstUsed[cardF.Children[1].Ref] {
			t.Fatalf("new card F reused an old ref (A=%s/%s/%s, F=%s/%s/%s)",
				cardA.Ref, cardA.Children[0].Ref, cardA.Children[1].Ref,
				cardF.Ref, cardF.Children[0].Ref, cardF.Children[1].Ref)
		}
	})

	t.Run("separated_identical_buttons_under_same_anchored_card_keep_refs", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, scrollCardsCapture([]string{"A", "B"}, 0, 2))
		second := buildOne(t, b, scrollCardsCapture([]string{"A", "B"}, 0, 2))
		for _, title := range []string{"A", "B"} {
			oldCard := findCardByTitle(first.Tree, title)
			newCard := findCardByTitle(second.Tree, title)
			if oldCard.Ref != newCard.Ref || oldCard.Children[0].Ref != newCard.Children[0].Ref {
				t.Fatalf("card %s or title drifted: %s -> %s", title, oldCard.Ref, newCard.Ref)
			}
			for i := 1; i < len(oldCard.Children); i++ {
				if oldCard.Children[i].Ref != newCard.Children[i].Ref {
					t.Fatalf("separated identical button %d under card %s renumbered: %s -> %s",
						i, title, oldCard.Children[i].Ref, newCard.Children[i].Ref)
				}
			}
		}
	})

	t.Run("overlapping_identical_buttons_under_same_anchored_card_get_fresh_refs", func(t *testing.T) {
		build := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
			card := c.add(scroll, withClass(visible(rect(0, 120, 1080, 460)), "android.widget.LinearLayout"))
			c.add(card, withText(withClass(visible(rect(20, 140, 800, 240)), "android.widget.TextView"), "A"))
			for b := 0; b < 2; b++ {
				btn := clickable(withRes(withClass(withText(visible(rect(820, 140, 1020, 240)), "收藏"), "android.widget.Button"), "app:id/fav"))
				c.add(card, btn)
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build())
		oldCard := findCardByTitle(first.Tree, "A")
		oldRefs := map[string]bool{oldCard.Children[1].Ref: true, oldCard.Children[2].Ref: true}
		second := buildOne(t, b, build())
		newCard := findCardByTitle(second.Tree, "A")
		if newCard.Ref != oldCard.Ref || newCard.Children[0].Ref != oldCard.Children[0].Ref {
			t.Fatalf("card or title drifted with identical buttons in one rectangle")
		}
		for _, fav := range newCard.Children[1:] {
			if oldRefs[fav.Ref] {
				t.Fatalf("overlapping ambiguous fav button reused old ref %s", fav.Ref)
			}
		}
	})

	t.Run("local_structure_remaining_one_rejected", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, scrollCardsCapture([]string{"OldOnly", "Keep"}, 0, 1))
		second := buildOne(t, b, scrollCardsCapture([]string{"Keep", "NewOnly"}, 0, 1))

		oldOnly := findCardByTitle(first.Tree, "OldOnly")
		newOnly := findCardByTitle(second.Tree, "NewOnly")
		if newOnly.Ref == oldOnly.Ref {
			t.Fatalf("remaining 1/1 anonymous card matched by Local-Structure despite 2/2 total siblings: %s", oldOnly.Ref)
		}
		if newOnly.Children[1].Ref == oldOnly.Children[1].Ref {
			t.Fatalf("remaining card button wrongly matched under rejected card: %s", oldOnly.Children[1].Ref)
		}
	})
}

func TestContinuityOrdinalRequiresIndependentFixedPoint(t *testing.T) {
	t.Run("fixed_point_at_rank1_allows_rank0_gap_fill_and_local_button_recovery", func(t *testing.T) {
		build := func(t0, t1 string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
			for idx, title := range []string{t0, t1} {
				y := 120 + idx*380
				card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+340)), "android.widget.LinearLayout"))
				c.add(card, withText(withClass(visible(rect(20, y+20, 700, y+120)), "android.widget.TextView"), title))
				c.add(card, clickable(withRes(withClass(withText(visible(rect(800, y+20, 1000, y+120)), "赞"), "android.widget.Button"), "app:id/like")))
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build("甲", "乙"))
		second := buildOne(t, b, build("丙", "乙"))

		oldCard0 := findCardByTitle(first.Tree, "甲")
		newCard0 := findCardByTitle(second.Tree, "丙")
		if newCard0.Ref != oldCard0.Ref {
			t.Fatalf("rank 0 card should match via rank 1 fixed point: %s -> %s", oldCard0.Ref, newCard0.Ref)
		}
		if newCard0.Children[0].Ref == oldCard0.Children[0].Ref {
			t.Fatalf("replaced title reused old ref %s", oldCard0.Children[0].Ref)
		}
		if newCard0.Children[1].Ref != oldCard0.Children[1].Ref {
			t.Fatalf("local button under ordinal-recovered card lost ref: %s -> %s",
				oldCard0.Children[1].Ref, newCard0.Children[1].Ref)
		}
	})

	t.Run("fixed_point_allows_post_ordinal_local_resource_counter_recovery", func(t *testing.T) {
		build := func(t0, c0, t1, c1 string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
			titles := []string{t0, t1}
			counts := []string{c0, c1}
			for idx := range titles {
				y := 120 + idx*380
				card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+340)), "android.widget.LinearLayout"))
				c.add(card, withText(withClass(visible(rect(20, y+20, 500, y+120)), "android.widget.TextView"), titles[idx]))
				c.add(card, withText(withRes(withClass(visible(rect(520, y+20, 760, y+120)), "android.widget.TextView"), "app:id/counter"), counts[idx]))
				c.add(card, clickable(withRes(withClass(withText(visible(rect(800, y+20, 1000, y+120)), "赞"), "android.widget.Button"), "app:id/like")))
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build("甲", "1", "乙", "9"))
		second := buildOne(t, b, build("丙", "2", "乙", "9"))

		oldCard0 := findCardByTitle(first.Tree, "甲")
		newCard0 := findCardByTitle(second.Tree, "丙")
		if newCard0.Ref != oldCard0.Ref {
			t.Fatalf("rank 0 card should match via rank 1 fixed point: %s -> %s", oldCard0.Ref, newCard0.Ref)
		}
		if newCard0.Children[0].Ref == oldCard0.Children[0].Ref {
			t.Fatalf("unresourced replaced title reused old ref %s", oldCard0.Children[0].Ref)
		}
		if newCard0.Children[1].Ref != oldCard0.Children[1].Ref {
			t.Fatalf("uniquely resourced counter under ordinal card lost ref: %s -> %s",
				oldCard0.Children[1].Ref, newCard0.Children[1].Ref)
		}
		if newCard0.Children[2].Ref != oldCard0.Children[2].Ref {
			t.Fatalf("local button under ordinal card lost ref: %s -> %s",
				oldCard0.Children[2].Ref, newCard0.Children[2].Ref)
		}
	})

	t.Run("all_content_replaced_with_no_fixed_point_gets_fresh_refs", func(t *testing.T) {
		build := func(t0, t1 string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
			for idx, title := range []string{t0, t1} {
				y := 120 + idx*380
				card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+340)), "android.widget.LinearLayout"))
				c.add(card, withText(withRes(withClass(visible(rect(20, y+20, 700, y+120)), "android.widget.TextView"), "app:id/tv_title"), title))
				c.add(card, clickable(withRes(withClass(withText(visible(rect(800, y+20, 1000, y+120)), "赞"), "android.widget.Button"), "app:id/like")))
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build("A", "B"))
		second := buildOne(t, b, build("C", "D"))
		oldRefs := map[string]bool{}
		for _, r := range first.Refs[2:] { // skip synthetic root and scroll
			oldRefs[r] = true
		}
		for _, r := range second.Refs[2:] {
			if oldRefs[r] {
				t.Fatalf("ordinal matched without independent fixed point: reused ref %s", r)
			}
		}
	})
}

func TestContinuityOrdinalRejectsShiftCrossGroupAndConflict(t *testing.T) {
	t.Run("shifted_fixed_point_rejects_entire_bucket", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, scrollCardsCapture([]string{"A", "B", "C"}, 0, 1))
		second := buildOne(t, b, scrollCardsCapture([]string{"X", "A", "Y"}, 0, 1))

		oldA := findCardByTitle(first.Tree, "A")
		newA := findCardByTitle(second.Tree, "A")
		if oldA.Ref != newA.Ref {
			t.Fatalf("shifted card A should keep ref via Stage 3: %s -> %s", oldA.Ref, newA.Ref)
		}
		oldBC := map[string]bool{
			findCardByTitle(first.Tree, "B").Ref: true,
			findCardByTitle(first.Tree, "C").Ref: true,
		}
		for _, title := range []string{"X", "Y"} {
			card := findCardByTitle(second.Tree, title)
			if oldBC[card.Ref] {
				t.Fatalf("shifted group allowed ordinal match for %s -> %s", title, card.Ref)
			}
		}
	})

	t.Run("cross_group_matched_member_rejects_entire_bucket", func(t *testing.T) {
		buildFrame := func(moved bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box1 := c.add(root, withRes(clickable(withClass(visible(rect(0, 0, 1080, 1100)), "android.widget.LinearLayout")), "app:id/box1"))
			box2 := c.add(root, withRes(clickable(withClass(visible(rect(0, 1120, 1080, 2200)), "android.widget.LinearLayout")), "app:id/box2"))

			// rank 0 under box1: content changes Old0 -> New0
			r0 := c.add(box1, withClass(visible(rect(10, 10, 1000, 300)), "android.widget.FrameLayout"))
			title0 := "Old0"
			if moved {
				title0 = "New0"
			}
			c.add(r0, withText(withClass(visible(rect(20, 20, 400, 80)), "android.widget.TextView"), title0))

			if !moved {
				// rank 1 under box1 in old frame: replaced in new frame by node moved from box2
				r1 := c.add(box1, withClass(visible(rect(10, 320, 1000, 600)), "android.widget.FrameLayout"))
				c.add(r1, withText(withClass(visible(rect(20, 330, 400, 390)), "android.widget.TextView"), "Old1"))
			} else {
				// Moved from box2 into box1 at rank 1 (unpublished resource id identifies it globally).
				rExt := c.add(box1, withRes(withClass(visible(rect(10, 320, 1000, 600)), "android.widget.FrameLayout"), "app:id/ext_container"))
				c.add(rExt, withText(withClass(visible(rect(20, 330, 400, 390)), "android.widget.TextView"), "ExtLeaf"))
			}

			// rank 2 under box1: independent fixed point at rank 2
			r2 := c.add(box1, withClass(visible(rect(10, 620, 1000, 900)), "android.widget.FrameLayout"))
			c.add(r2, withText(withClass(visible(rect(20, 630, 400, 690)), "android.widget.TextView"), "FixedRank2"))

			if !moved {
				rExt := c.add(box2, withRes(withClass(visible(rect(10, 1130, 1000, 1400)), "android.widget.FrameLayout"), "app:id/ext_container"))
				c.add(rExt, withText(withClass(visible(rect(20, 1140, 400, 1200)), "android.widget.TextView"), "ExtLeaf"))
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, buildFrame(false))
		oldR0 := findCardByTitle(first.Tree, "Old0").Ref
		second := buildOne(t, b, buildFrame(true))
		newR0 := findCardByTitle(second.Tree, "New0").Ref
		if newR0 == oldR0 {
			t.Fatalf("cross-group match inside bucket failed to reject ordinal: %s", oldR0)
		}
	})

	t.Run("old_member_matched_outside_group_rejects_entire_bucket", func(t *testing.T) {
		buildFrame := func(movedOut bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box1 := c.add(root, withRes(clickable(withClass(visible(rect(0, 0, 1080, 1100)), "android.widget.LinearLayout")), "app:id/box1"))
			box2 := c.add(root, withRes(clickable(withClass(visible(rect(0, 1120, 1080, 2200)), "android.widget.LinearLayout")), "app:id/box2"))

			r0 := c.add(box1, withClass(visible(rect(10, 10, 1000, 300)), "android.widget.FrameLayout"))
			title0 := "Old0"
			if movedOut {
				title0 = "New0"
			}
			c.add(r0, withText(withClass(visible(rect(20, 20, 400, 80)), "android.widget.TextView"), title0))

			if !movedOut {
				rExt := c.add(box1, withRes(withClass(visible(rect(10, 320, 1000, 600)), "android.widget.FrameLayout"), "app:id/ext_container"))
				c.add(rExt, withText(withClass(visible(rect(20, 330, 400, 390)), "android.widget.TextView"), "ExtLeaf"))
			} else {
				r1 := c.add(box1, withClass(visible(rect(10, 320, 1000, 600)), "android.widget.FrameLayout"))
				c.add(r1, withText(withClass(visible(rect(20, 330, 400, 390)), "android.widget.TextView"), "New1"))
			}

			r2 := c.add(box1, withClass(visible(rect(10, 620, 1000, 900)), "android.widget.FrameLayout"))
			c.add(r2, withText(withClass(visible(rect(20, 630, 400, 690)), "android.widget.TextView"), "FixedRank2"))

			if movedOut {
				rExt := c.add(box2, withRes(withClass(visible(rect(10, 1130, 1000, 1400)), "android.widget.FrameLayout"), "app:id/ext_container"))
				c.add(rExt, withText(withClass(visible(rect(20, 1140, 400, 1200)), "android.widget.TextView"), "ExtLeaf"))
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, buildFrame(false))
		oldR0 := findCardByTitle(first.Tree, "Old0").Ref
		second := buildOne(t, b, buildFrame(true))
		newR0 := findCardByTitle(second.Tree, "New0").Ref
		if newR0 == oldR0 {
			t.Fatalf("old bucket member matched outside group failed to reject ordinal: %s", oldR0)
		}
	})

	t.Run("reverse_conflict_rejects_batch_without_partial_apply", func(t *testing.T) {
		buildButtons := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			c.add(root, clickable(withRes(withClass(withText(visible(rect(20, 120, 300, 220)), "A"), "android.widget.Button"), "app:id/btn_a")))
			c.add(root, clickable(withRes(withClass(withText(visible(rect(20, 260, 300, 360)), "B"), "android.widget.Button"), "app:id/btn_b")))
			c.add(root, clickable(withRes(withClass(withText(visible(rect(20, 400, 300, 500)), "C"), "android.widget.Button"), "app:id/btn_c")))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, buildButtons())
		cap2 := buildButtons()
		idx := buildPublicMatchIndex(first, cap2, project(cap2))
		prevBtns := idx.prevChildren[idx.prevRoot]
		curBtns := idx.childrenOf(idx.root)
		if len(prevBtns) != 3 || len(curBtns) != 3 {
			t.Fatalf("expected 3 buttons per frame, got %d and %d", len(prevBtns), len(curBtns))
		}

		checkBatchRejected := func(name string, seedRoot bool, proposals []pairProposal, limit uint64, wantOK bool) {
			t.Helper()
			st := newMatchState(idx, make([]string, len(cap2.Nodes)))
			wantMatched := 0
			if seedRoot {
				st.assign(curBtns[0], prevBtns[0], sourceExact)
				wantMatched = 1
			}
			baseR := st.snapshotPairs()
			accepted, ok := st.chooseBatchIfBidirectionallyUnique(proposals, baseR, newWorkBudget(limit))
			if ok != wantOK {
				t.Fatalf("%s: ok = %v, want %v", name, ok, wantOK)
			}
			if len(accepted) != 0 {
				t.Fatalf("%s: accepted %d proposals on invalid/aborted batch", name, len(accepted))
			}
			if st.matchCount() != wantMatched {
				t.Fatalf("%s: matchCount mutated from %d to %d", name, wantMatched, st.matchCount())
			}
		}

		checkBatchRejected("duplicate_prevPos", false, []pairProposal{
			{prevPos: prevBtns[0], cur: curBtns[0], source: sourceOrdinal},
			{prevPos: prevBtns[0], cur: curBtns[1], source: sourceOrdinal},
			{prevPos: prevBtns[2], cur: curBtns[2], source: sourceOrdinal},
		}, 100, true)

		checkBatchRejected("duplicate_cur", false, []pairProposal{
			{prevPos: prevBtns[0], cur: curBtns[0], source: sourceOrdinal},
			{prevPos: prevBtns[1], cur: curBtns[0], source: sourceOrdinal},
			{prevPos: prevBtns[2], cur: curBtns[2], source: sourceOrdinal},
		}, 100, true)

		checkBatchRejected("already_used_in_baseR", true, []pairProposal{
			{prevPos: prevBtns[0], cur: curBtns[1], source: sourceOrdinal},
			{prevPos: prevBtns[2], cur: curBtns[2], source: sourceOrdinal},
		}, 100, true)

		checkBatchRejected("budget_exhausted_mid_batch", false, []pairProposal{
			{prevPos: prevBtns[0], cur: curBtns[0], source: sourceOrdinal},
			{prevPos: prevBtns[1], cur: curBtns[1], source: sourceOrdinal},
			{prevPos: prevBtns[2], cur: curBtns[2], source: sourceOrdinal},
		}, 2, false)
	})
}

func TestContinuityOrdinalCannotSeedAnotherBucket(t *testing.T) {
	build := func(g1a, g1b, g2a, g2b string) *device.Capture {
		c := newCap(1080, 2400)
		root := c.root()
		scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
		// Shape bucket G1: non-clickable LinearLayout
		for idx, title := range []string{g1a, g1b} {
			y := 120 + idx*200
			row := c.add(scroll, withClass(visible(rect(0, y, 1080, y+180)), "android.widget.LinearLayout"))
			c.add(row, withText(withClass(visible(rect(20, y+20, 600, y+100)), "android.widget.TextView"), title))
		}
		// Shape bucket G2: clickable FrameLayout (distinct shapeKey via action + class)
		for idx, title := range []string{g2a, g2b} {
			y := 600 + idx*200
			row := c.add(scroll, clickable(withClass(visible(rect(0, y, 1080, y+180)), "android.widget.FrameLayout")))
			c.add(row, withText(withClass(visible(rect(20, y+20, 600, y+100)), "android.widget.TextView"), title))
		}
		return c.capture()
	}

	b := NewBuilder()
	first := buildOne(t, b, build("G1_Old", "G1_Fixed", "G2_A", "G2_B"))
	second := buildOne(t, b, build("G1_New", "G1_Fixed", "G2_C", "G2_D"))

	if findCardByTitle(first.Tree, "G1_Old").Ref != findCardByTitle(second.Tree, "G1_New").Ref {
		t.Fatalf("G1 with fixed point should fill rank 0: %s -> %s",
			findCardByTitle(first.Tree, "G1_Old").Ref, findCardByTitle(second.Tree, "G1_New").Ref)
	}
	oldG2 := map[string]bool{
		findCardByTitle(first.Tree, "G2_A").Ref: true,
		findCardByTitle(first.Tree, "G2_B").Ref: true,
	}
	for _, title := range []string{"G2_C", "G2_D"} {
		if oldG2[findCardByTitle(second.Tree, title).Ref] {
			t.Fatalf("G2 without fixed point reused ref from G1 ordinal pass: %s", title)
		}
	}
}

func TestContinuityPostOrdinalLocalOnly(t *testing.T) {
	t.Run("ordinal_card_recovers_local_button_without_rerunning_descendant_vote", func(t *testing.T) {
		build := func(movedStray bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
			if !movedStray {
				c.add(root, withText(withClass(visible(rect(0, 0, 200, 80)), "android.widget.TextView"), "Stray"))
			}
			// Rank 0 card0: cannot match in Stage 3 when Stray moves into it (cross-anchor).
			card0 := c.add(scroll, withClass(visible(rect(0, 120, 1080, 700)), "android.widget.LinearLayout"))
			card0Title := "OldCard0"
			if movedStray {
				card0Title = "NewCard0"
			}
			c.add(card0, withText(withRes(withClass(visible(rect(10, 130, 500, 200)), "android.widget.TextView"), "app:id/card_title"), card0Title))
			c.add(card0, clickable(withRes(withClass(withText(visible(rect(600, 130, 800, 200)), "收藏"), "android.widget.Button"), "app:id/fav")))
			if movedStray {
				c.add(card0, withText(withClass(visible(rect(10, 210, 210, 280)), "android.widget.TextView"), "Stray"))
			}
			// Inside card0: two same-shape sub-containers. subA holds globally unique leaf;
			// if Stage 3 re-ran after Stage 5 anchored card0, subA would wrongly match after ordinal.
			subA := c.add(card0, withClass(visible(rect(10, 300, 500, 450)), "android.widget.FrameLayout"))
			c.add(subA, withText(withClass(visible(rect(20, 310, 400, 380)), "android.widget.TextView"), "UniqueSubALeaf"))
			subB := c.add(card0, withClass(visible(rect(10, 470, 500, 620)), "android.widget.FrameLayout"))
			subBLabel := "SubB_Old"
			if movedStray {
				subBLabel = "SubB_New"
			}
			c.add(subB, withText(withClass(visible(rect(20, 480, 400, 550)), "android.widget.TextView"), subBLabel))

			// Rank 1 card1: fixed point for Stage 5
			card1 := c.add(scroll, withClass(visible(rect(0, 720, 1080, 1100)), "android.widget.LinearLayout"))
			c.add(card1, withText(withRes(withClass(visible(rect(10, 730, 500, 800)), "android.widget.TextView"), "app:id/card_title"), "KeepCard1"))
			c.add(card1, clickable(withRes(withClass(withText(visible(rect(600, 730, 800, 800)), "收藏"), "android.widget.Button"), "app:id/fav")))
			return c.capture()
		}

		b := NewBuilder()
		first := buildOne(t, b, build(false))
		second := buildOne(t, b, build(true))

		oldCard0 := findCardByTitle(first.Tree, "OldCard0")
		newCard0 := findCardByTitle(second.Tree, "NewCard0")
		if newCard0.Ref != oldCard0.Ref {
			t.Fatalf("card0 should match at rank 0 via Stage 5: %s -> %s", oldCard0.Ref, newCard0.Ref)
		}
		if newCard0.Children[1].Ref != oldCard0.Children[1].Ref {
			t.Fatalf("post-ordinal local recovery failed to preserve fav button: %s -> %s",
				oldCard0.Children[1].Ref, newCard0.Children[1].Ref)
		}
		oldSubA := findCardByTitle(first.Tree, "UniqueSubALeaf")
		newSubA := findCardByTitle(second.Tree, "UniqueSubALeaf")
		if newSubA.Ref == oldSubA.Ref {
			t.Fatalf("Stage 3 must not rerun after Stage 5 to match subA (%s)", oldSubA.Ref)
		}
	})

	t.Run("ordinal_does_not_run_a_second_time_on_newly_anchored_children", func(t *testing.T) {
		build := func(secondFrame bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))

			card0 := c.add(scroll, withClass(visible(rect(0, 120, 1080, 700)), "android.widget.LinearLayout"))
			c0Title := "Card0_Old"
			sub0Title := "Sub0_Old"
			if secondFrame {
				c0Title = "Card0_New"
				sub0Title = "Sub0_New"
			}
			c.add(card0, withText(withRes(withClass(visible(rect(10, 130, 500, 200)), "android.widget.TextView"), "app:id/card_title"), c0Title))
			sub0 := c.add(card0, withClass(visible(rect(10, 220, 500, 350)), "android.widget.FrameLayout"))
			c.add(sub0, withText(withClass(visible(rect(20, 230, 400, 300)), "android.widget.TextView"), sub0Title))
			// sub1 has unpublished resource id, so post-ordinal Local-Exact matches sub1 at rank 1.
			// A second Stage 5 run would falsely match sub0 at rank 0; single-run Stage 5 must not.
			sub1 := c.add(card0, withRes(withClass(visible(rect(10, 370, 500, 500)), "android.widget.FrameLayout"), "app:id/sub1_box"))
			sub1Label := "Sub1_Old"
			if secondFrame {
				sub1Label = "Sub1_New"
			}
			c.add(sub1, withText(withClass(visible(rect(20, 380, 400, 450)), "android.widget.TextView"), sub1Label))

			card1 := c.add(scroll, withClass(visible(rect(0, 720, 1080, 1100)), "android.widget.LinearLayout"))
			c.add(card1, withText(withRes(withClass(visible(rect(10, 730, 500, 800)), "android.widget.TextView"), "app:id/card_title"), "Card1_Keep"))
			// Duplicate sub1 template in card1 so Stage 2 global passes cannot anchor sub1 inside card0 prematurely.
			card1Sub := c.add(card1, withRes(withClass(visible(rect(10, 820, 500, 950)), "android.widget.FrameLayout"), "app:id/sub1_box"))
			card1SubLabel := "Card1Sub_Old"
			if secondFrame {
				card1SubLabel = "Card1Sub_New"
			}
			c.add(card1Sub, withText(withClass(visible(rect(20, 830, 400, 900)), "android.widget.TextView"), card1SubLabel))
			return c.capture()
		}

		b := NewBuilder()
		first := buildOne(t, b, build(false))
		second := buildOne(t, b, build(true))

		oldSub0 := findCardByTitle(first.Tree, "Sub0_Old")
		newSub0 := findCardByTitle(second.Tree, "Sub0_New")
		if newSub0.Ref == oldSub0.Ref {
			t.Fatalf("Stage 5 must run only once and not seed nested ordinal match for sub0 (%s)", oldSub0.Ref)
		}
		oldSub1 := findCardByTitle(first.Tree, "Sub1_Old")
		newSub1 := findCardByTitle(second.Tree, "Sub1_New")
		if newSub1 == nil || oldSub1 == nil || newSub1.Ref != oldSub1.Ref {
			t.Fatalf("post-ordinal Local-Exact must recover sub1 under ordinal-matched card0: old=%v new=%v", oldSub1, newSub1)
		}
	})
}

func TestContinuitySyntheticRootUsesCaptureRootAndRollback(t *testing.T) {
	t.Run("nonzero_capture_root_continues_across_raw_class_package_changes", func(t *testing.T) {
		makeNonZeroRoot := func(className, pkg, label string) *device.Capture {
			nodes := []device.RawNode{
				{Index: 0, Parent: -1, Visible: false},
				{Index: 1, Parent: -1, Visible: false},
				{
					Index: 2, Parent: -1, Children: []int{3}, Visible: true,
					ClassName: className, Package: pkg,
					RawBounds: rect(0, 0, 1080, 2400), Bounds: rect(0, 0, 1080, 2400),
				},
				withText(withClass(visible(rect(10, 10, 300, 80)), "android.widget.TextView"), label),
			}
			nodes[3].Index = 3
			nodes[3].Parent = 2
			nodes[3].Depth = 1
			return &device.Capture{
				Display: device.Display{ID: 0, Width: 1080, Height: 2400},
				Nodes:   nodes,
				Root:    2,
			}
		}
		b := NewBuilder()
		first := buildOne(t, b, makeNonZeroRoot("com.a.RootA", "com.a", "one"))
		second := buildOne(t, b, makeNonZeroRoot("com.b.RootB", "com.b", "two"))
		if first.Tree.Root.Ref != second.Tree.Root.Ref {
			t.Fatalf("non-zero capture root renumbered: %s -> %s", first.Tree.Root.Ref, second.Tree.Root.Ref)
		}
		if got := second.Source[second.Tree.Root.Ref]; got != 2 {
			t.Fatalf("root source index = %d, want 2", got)
		}
	})

	t.Run("high_watermark_never_recalculates_from_visible_max_and_candidate_rolls_back", func(t *testing.T) {
		buildMany := func(count int) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			for i := 0; i < count; i++ {
				c.add(root, withText(withRes(withClass(visible(rect(0, i*20, 300, i*20+18)), "android.widget.TextView"), fmt.Sprintf("app:id/i%d", i)), fmt.Sprintf("item-%d", i)))
			}
			return c.capture()
		}
		buildSparseWithExtra := func(extra string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			c.add(root, withText(withRes(withClass(visible(rect(0, 0, 300, 20)), "android.widget.TextView"), "app:id/i9"), "item-9"))
			if extra != "" {
				c.add(root, withText(withRes(withClass(visible(rect(0, 40, 300, 60)), "android.widget.TextView"), "app:id/extra"), extra))
			}
			return c.capture()
		}

		b := NewBuilder()
		first := buildOne(t, b, buildMany(19)) // n1..n20; b.nextRef == 21
		if len(first.Refs) != 20 || first.Refs[19] != "n20" {
			t.Fatalf("unexpected initial refs: %v", first.Refs)
		}
		sparse := buildOne(t, b, buildSparseWithExtra(""))
		if sparse.Refs[0] != "n1" || sparse.Refs[1] != "n11" {
			t.Fatalf("sparse refs = %v, want [n1 n11]", sparse.Refs)
		}

		discarded, _, err := b.Candidate(buildSparseWithExtra("cand"))
		if err != nil {
			t.Fatalf("Candidate: %v", err)
		}
		if discarded.Refs[1] != "n11" || discarded.Refs[2] != "n21" {
			t.Fatalf("candidate refs = %v, want [n1 n11 n21] (must not reuse n12)", discarded.Refs)
		}
		if _, err := b.Preview(buildSparseWithExtra("prev")); err != nil {
			t.Fatalf("Preview: %v", err)
		}
		committed := buildOne(t, b, buildSparseWithExtra("real"))
		if committed.Refs[1] != "n11" || committed.Refs[2] != "n21" {
			t.Fatalf("committed refs after discarded Candidate/Preview = %v, want [n1 n11 n21]", committed.Refs)
		}
	})
}

func TestContinuityBudgetCutoffDeterministicAndConservative(t *testing.T) {
	buildSample := func(reverseRaw bool) *device.Capture {
		c := newCap(1080, 2400)
		root := c.root()
		scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
		for i := 0; i < 4; i++ {
			y := 120 + i*350
			card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+300)), "android.widget.LinearLayout"))
			c.add(card, withText(withRes(withClass(visible(rect(10, y+10, 600, y+80)), "android.widget.TextView"), "app:id/t"), fmt.Sprintf("card-%d", i)))
			c.add(card, clickable(withRes(withClass(withText(visible(rect(700, y+10, 900, y+80)), "按钮"), "android.widget.Button"), "app:id/btn")))
		}
		raw := c.capture()
		if !reverseRaw {
			return raw
		}
		return reorderCaptureRawNonRootReverse(raw)
	}

	b1 := NewBuilder()
	_ = buildOne(t, b1, buildSample(false))
	out1 := buildOne(t, b1, buildSample(false))
	assertUniqueTreeRefs(t, out1)

	for run := 0; run < 5; run++ {
		b2 := NewBuilder()
		_ = buildOne(t, b2, buildSample(false))
		out2 := buildOne(t, b2, buildSample(true))
		assertUniqueTreeRefs(t, out2)
		for i := range out1.Refs {
			if out1.Refs[i] != out2.Refs[i] {
				t.Fatalf("run %d drifted at %d: %s vs %s", run, i, out1.Refs[i], out2.Refs[i])
			}
		}
	}

	t.Run("budget_limit_formula_and_atomic_cutoff_sweep", func(t *testing.T) {
		buildAtomicSample := func(secondFrame bool, reverseRaw bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
			titles := []string{"固定", "甲", "乙"}
			if secondFrame {
				titles = []string{"固定", "丙", "丁"}
			}
			for idx, title := range titles {
				y := 120 + idx*380
				card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+340)), "android.widget.LinearLayout"))
				if idx == 0 {
					c.add(card, withText(withRes(withClass(visible(rect(20, y+20, 700, y+120)), "android.widget.TextView"), "app:id/fixed_title"), title))
					c.add(card, clickable(withRes(withClass(withText(visible(rect(800, y+20, 1000, y+120)), "固定赞"), "android.widget.Button"), "app:id/fixed_like")))
					continue
				}
				c.add(card, withText(withClass(visible(rect(20, y+20, 700, y+120)), "android.widget.TextView"), title))
				c.add(card, clickable(withRes(withClass(withText(visible(rect(800, y+20, 1000, y+120)), "赞"), "android.widget.Button"), "app:id/like")))
			}
			raw := c.capture()
			if !reverseRaw {
				return raw
			}
			return reorderCaptureRawNonRootReverse(raw)
		}

		bFull := NewBuilder()
		prevFull := buildOne(t, bFull, buildAtomicSample(false, false))
		fullSnap := buildOne(t, bFull, buildAtomicSample(true, false))
		fullStats := bFull.lastStats
		wantFormula := uint64(128*(len(prevFull.Refs)+len(fullSnap.Refs)+1) + 65_536)
		if fullStats.workLimit != wantFormula {
			t.Fatalf("workLimit = %d, want Wmax %d", fullStats.workLimit, wantFormula)
		}
		if fullStats.stage5Matched != 2 {
			t.Fatalf("expected atomic ordinal batch of 2 cards, got stage5Matched=%d (stats=%+v)", fullStats.stage5Matched, fullStats)
		}

		prevRefSet := make(map[string]bool, len(prevFull.Refs))
		for _, ref := range prevFull.Refs {
			prevRefSet[ref] = true
		}

		ordinalCommitLimit := uint64(0)
		foundCommit := false
		var statsAtNMinus1 matchStats
		var snapAtNMinus1 *Snapshot
		var snapAtN *Snapshot

		for limit := uint64(0); limit <= fullStats.workUsed; limit++ {
			bNorm := NewBuilder()
			_ = buildOne(t, bNorm, buildAtomicSample(false, false))
			bNorm.hasBudgetLimit = true
			bNorm.budgetLimit = limit
			outNorm := buildOne(t, bNorm, buildAtomicSample(true, false))
			stNorm := bNorm.lastStats

			bRev := NewBuilder()
			_ = buildOne(t, bRev, buildAtomicSample(false, false))
			bRev.hasBudgetLimit = true
			bRev.budgetLimit = limit
			outRev := buildOne(t, bRev, buildAtomicSample(true, true))
			stRev := bRev.lastStats

			assertUniqueTreeRefs(t, outNorm)
			assertUniqueTreeRefs(t, outRev)
			if stNorm.workUsed > limit {
				t.Fatalf("limit=%d exceeded work budget: workUsed=%d", limit, stNorm.workUsed)
			}
			if stNorm != stRev {
				t.Fatalf("limit=%d reverseRaw drifted stats: %+v vs %+v", limit, stNorm, stRev)
			}
			if len(outNorm.Refs) != len(outRev.Refs) {
				t.Fatalf("limit=%d ref len mismatch", limit)
			}
			for i := range outNorm.Refs {
				if outNorm.Refs[i] != outRev.Refs[i] {
					t.Fatalf("limit=%d reverseRaw drifted ref[%d]: %s vs %s", limit, i, outNorm.Refs[i], outRev.Refs[i])
				}
			}
			if stNorm.stage5Matched == 1 {
				t.Fatalf("limit=%d allowed partial ordinal batch: stage5Matched=1", limit)
			}
			if !foundCommit && stNorm.stage5Matched == 2 {
				foundCommit = true
				ordinalCommitLimit = limit
				snapAtN = outNorm
			}
			if !foundCommit {
				statsAtNMinus1 = stNorm
				snapAtNMinus1 = outNorm
			}
		}

		if !foundCommit || ordinalCommitLimit == 0 {
			t.Fatalf("failed to observe ordinal commit threshold in 0..%d", fullStats.workUsed)
		}
		if !statsAtNMinus1.budgetHit || statsAtNMinus1.stage5Matched != 0 {
			t.Fatalf("limit N-1 (%d) expected budgetHit with stage5Matched=0, got %+v", ordinalCommitLimit-1, statsAtNMinus1)
		}
		prevRank0Card := prevFull.Tree.Root.Children[0].Children[0]
		curScrollMinus1 := snapAtNMinus1.Tree.Root.Children[0]
		if curScrollMinus1.Children[0].Ref != prevRank0Card.Ref {
			t.Fatalf("rank-0 prior match lost ref at N-1 cutoff: %s -> %s", prevRank0Card.Ref, curScrollMinus1.Children[0].Ref)
		}
		for idx := 1; idx <= 2; idx++ {
			cardMinus1 := curScrollMinus1.Children[idx]
			if prevRefSet[cardMinus1.Ref] {
				t.Fatalf("card %d reused old ref %s at N-1 cutoff", idx, cardMinus1.Ref)
			}
			for _, ch := range cardMinus1.Children {
				if prevRefSet[ch.Ref] {
					t.Fatalf("child under card %d reused old ref %s at N-1 cutoff", idx, ch.Ref)
				}
			}
		}
		curScrollN := snapAtN.Tree.Root.Children[0]
		for idx := 1; idx <= 2; idx++ {
			if curScrollN.Children[idx].Ref != prevFull.Tree.Root.Children[0].Children[idx].Ref {
				t.Fatalf("card %d did not recover atomically at N=%d: got %s, want %s",
					idx, ordinalCommitLimit, curScrollN.Children[idx].Ref, prevFull.Tree.Root.Children[0].Children[idx].Ref)
			}
		}
		lastFresh := 0
		for _, ref := range snapAtNMinus1.Refs {
			if prevRefSet[ref] {
				continue
			}
			var seq int
			if _, err := fmt.Sscanf(ref, "n%d", &seq); err != nil {
				t.Fatalf("unexpected fresh ref format %q: %v", ref, err)
			}
			if seq <= lastFresh {
				t.Fatalf("fresh refs at N-1 cutoff not strictly monotonic in preorder: %d after %d", seq, lastFresh)
			}
			lastFresh = seq
		}
	})
}

// reorderCaptureRawNonRootReverse reverses non-root RawNode storage order to verify
// matching and fresh allocation depend only on tree order, never raw array indices.
func reorderCaptureRawNonRootReverse(raw *device.Capture) *device.Capture {
	cpCap := *raw
	n := len(raw.Nodes)
	remap := make([]int, n)
	remap[0] = 0
	for i := 1; i < n; i++ {
		remap[i] = n - i
	}
	reordered := make([]device.RawNode, n)
	for oldIdx, node := range raw.Nodes {
		newIdx := remap[oldIdx]
		cp := node
		cp.Index = newIdx
		if cp.Parent >= 0 {
			cp.Parent = remap[cp.Parent]
		}
		if len(node.Children) > 0 {
			cp.Children = make([]int, len(node.Children))
			for j, ch := range node.Children {
				cp.Children[j] = remap[ch]
			}
		}
		reordered[newIdx] = cp
	}
	cpCap.Nodes = reordered
	return &cpCap
}

func TestContinuityRepeatedResourceAndContentBoundaries(t *testing.T) {
	t.Run("shared_resource_distinct_labels_survive_reorder", func(t *testing.T) {
		build := func(labels []string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
			for i, label := range labels {
				y := 120 + i*300
				card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+250)), "android.widget.LinearLayout"))
				c.add(card, withText(withRes(withClass(visible(rect(10, y+10, 600, y+90)), "android.widget.TextView"), "app:id/row_title"), label))
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build([]string{"商品A", "商品B", "商品C"}))
		second := buildOne(t, b, build([]string{"商品C", "商品A", "商品B"}))
		for _, label := range []string{"商品A", "商品B", "商品C"} {
			if refOf(first, label) != refOf(second, label) {
				t.Fatalf("label %q with shared resource lost ref on reorder: %s -> %s",
					label, refOf(first, label), refOf(second, label))
			}
			if findCardByTitle(first.Tree, label).Ref != findCardByTitle(second.Tree, label).Ref {
				t.Fatalf("card for %q lost ref on reorder", label)
			}
		}
	})

	t.Run("repeated_resource_and_label_rows_get_fresh_refs", func(t *testing.T) {
		build := func() *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2200)), "android.widget.ScrollView")))
			for i := 0; i < 2; i++ {
				y := 120 + i*300
				card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+250)), "android.widget.LinearLayout"))
				c.add(card, withText(withRes(withClass(visible(rect(10, y+10, 600, y+90)), "android.widget.TextView"), "app:id/row_title"), "完全相同"))
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build())
		second := buildOne(t, b, build())
		oldSet := map[string]bool{}
		for _, card := range first.Tree.Root.Children[0].Children {
			oldSet[card.Ref] = true
			oldSet[card.Children[0].Ref] = true
		}
		for _, card := range second.Tree.Root.Children[0].Children {
			if oldSet[card.Ref] || oldSet[card.Children[0].Ref] {
				t.Fatalf("identical repeated row without fixed point reused old ref")
			}
		}
	})

	t.Run("resource_set_false_or_empty_resource_does_not_create_resource_identity", func(t *testing.T) {
		makeBtn := func(resSet bool, resID string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			btn := clickable(withClass(visible(rect(10, 10, 110, 110)), "android.widget.Button"))
			btn.ResourceSet = resSet
			btn.ResourceID = resID
			c.add(root, btn)
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, makeBtn(false, "app:id/ghost"))
		if first.Tree.Root.Children[0].Resource != nil {
			t.Fatalf("ResourceSet=false must not publish Resource: %v", first.Tree.Root.Children[0].Resource)
		}
		second := buildOne(t, b, makeBtn(true, "app:id/ghost"))
		if first.Tree.Root.Children[0].Ref == second.Tree.Root.Children[0].Ref {
			t.Fatalf("ResourceSet=false node must not match ResourceSet=true node")
		}

		makeMovedEmptyBtn := func(parentID string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box := c.add(root, withRes(clickable(withClass(visible(rect(0, 0, 500, 300)), "android.widget.LinearLayout")), parentID))
			btn := clickable(withClass(visible(rect(10, 10, 110, 110)), "android.widget.Button"))
			btn.ResourceSet = true
			btn.ResourceID = ""
			c.add(box, btn)
			return c.capture()
		}

		bEmpty := NewBuilder()
		e1 := buildOne(t, bEmpty, makeMovedEmptyBtn("app:id/box_a"))
		e2 := buildOne(t, bEmpty, makeMovedEmptyBtn("app:id/box_b"))
		if e1.Tree.Root.Children[0].Children[0].Resource != nil {
			t.Fatalf("empty ResourceID must not publish Resource: %v", e1.Tree.Root.Children[0].Children[0].Resource)
		}
		if e1.Tree.Root.Children[0].Children[0].Ref == e2.Tree.Root.Children[0].Children[0].Ref {
			t.Fatalf("empty ResourceID unlabelled leaf must not forge global/resource identity across parents")
		}
	})

	t.Run("text_set_and_desc_set_false_ignore_ghost_strings", func(t *testing.T) {
		makeMovedBtn := func(parentID string, textSet bool, label string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box := c.add(root, withRes(clickable(withClass(visible(rect(0, 0, 500, 300)), "android.widget.LinearLayout")), parentID))
			n := clickable(withRes(withClass(visible(rect(10, 10, 400, 100)), "android.widget.Button"), "app:id/item"))
			n.TextSet = textSet
			n.Text = label
			c.add(box, n)
			return c.capture()
		}
		bText := NewBuilder()
		t1 := buildOne(t, bText, makeMovedBtn("app:id/box_a", false, "GhostA"))
		btn1 := t1.Tree.Root.Children[0].Children[0]
		if len(btn1.Label) != 0 {
			t.Fatalf("TextSet=false leaked ghost label: %v", btn1.Label)
		}
		t2 := buildOne(t, bText, makeMovedBtn("app:id/box_b", true, "GhostA"))
		btn2 := t2.Tree.Root.Children[0].Children[0]
		if btn1.Ref == btn2.Ref {
			t.Fatalf("TextSet=false ghost text matched TextSet=true real text across parents: %s", btn1.Ref)
		}

		makeInput := func(descSet bool, desc string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			boxID := "app:id/in_box_1"
			if descSet {
				boxID = "app:id/in_box_2"
			}
			p1 := c.add(root, withRes(clickable(withClass(visible(rect(0, 0, 500, 300)), "android.widget.LinearLayout")), boxID))
			in := editable(withClass(visible(rect(10, 10, 400, 100)), "android.widget.EditText"))
			in.DescSet = descSet
			in.Description = desc
			c.add(p1, in)
			return c.capture()
		}
		bIn := NewBuilder()
		in1 := buildOne(t, bIn, makeInput(false, "GhostHint"))
		in2 := buildOne(t, bIn, makeInput(true, "GhostHint"))
		if in1.Tree.Root.Children[0].Children[0].Ref == in2.Tree.Root.Children[0].Children[0].Ref {
			t.Fatalf("DescSet=false ghost description matched DescSet=true real description on input")
		}
	})
}

func TestContinuityActionsAndProtectedBreakInputIdentity(t *testing.T) {
	t.Run("actions_mask_change_breaks_identity_across_roles_and_shapes", func(t *testing.T) {
		makeInput := func(longClick bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			in := withDesc(withRes(editable(withClass(visible(rect(10, 10, 800, 100)), "android.widget.EditText")), "app:id/input"), "用户名")
			if longClick {
				in = longClickable(in)
			}
			c.add(root, in)
			return c.capture()
		}
		b := NewBuilder()
		i1 := buildOne(t, b, makeInput(false))
		i2 := buildOne(t, b, makeInput(true))
		if i1.Tree.Root.Children[0].Ref == i2.Tree.Root.Children[0].Ref {
			t.Fatalf("input kept ref %s when long_press action changed", i1.Tree.Root.Children[0].Ref)
		}

		makeContainer := func(longClick bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box := withClass(visible(rect(0, 0, 1000, 400)), "android.widget.LinearLayout")
			if longClick {
				box = longClickable(box)
			}
			boxIdx := c.add(root, box)
			c.add(boxIdx, withText(withClass(visible(rect(10, 10, 300, 80)), "android.widget.TextView"), "inside"))
			return c.capture()
		}
		bc := NewBuilder()
		c1 := buildOne(t, bc, makeContainer(false))
		c2 := buildOne(t, bc, makeContainer(true))
		if c1.Tree.Root.Children[0].Ref == c2.Tree.Root.Children[0].Ref {
			t.Fatalf("anonymous container kept ref %s when actions changed", c1.Tree.Root.Children[0].Ref)
		}
	})

	t.Run("protected_change_breaks_identity", func(t *testing.T) {
		makeInput := func(isPwd bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			in := withRes(editable(withClass(visible(rect(10, 10, 800, 100)), "android.widget.EditText")), "app:id/secret")
			in.HasPassword = true
			in.Password = isPwd
			c.add(root, in)
			return c.capture()
		}
		b := NewBuilder()
		p1 := buildOne(t, b, makeInput(false))
		p2 := buildOne(t, b, makeInput(true))
		if p1.Tree.Root.Children[0].Ref == p2.Tree.Root.Children[0].Ref {
			t.Fatalf("input kept ref %s when password protection changed", p1.Tree.Root.Children[0].Ref)
		}
	})

	t.Run("unknown_presence_flags_do_not_forge_tap_or_protected", func(t *testing.T) {
		makeGhostFlags := func(known bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			node := withText(withRes(withClass(visible(rect(10, 10, 400, 100)), "android.widget.TextView"), "app:id/item"), "Item")
			node.HasClickable = known
			node.Clickable = true
			node.HasLongClick = known
			node.LongClickable = true
			node.HasScrollable = known
			node.Scrollable = true
			node.HasPassword = known
			node.Password = true
			c.add(root, node)
			return c.capture()
		}
		b := NewBuilder()
		ghost := buildOne(t, b, makeGhostFlags(false))
		n := ghost.Tree.Root.Children[0]
		if len(n.Actions) != 0 {
			t.Fatalf("unknown Has* flags forged actions: %v", n.Actions)
		}
		if n.Protected != nil {
			t.Fatalf("HasPassword=false must keep Protected=nil, got %v", *n.Protected)
		}
		if n.Enabled != nil || n.Checked != nil || n.Focused != nil || n.Selected != nil {
			t.Fatalf("unknown flags must stay nil in public node: %+v", n)
		}
		real := buildOne(t, b, makeGhostFlags(true))
		if real.Tree.Root.Children[0].Ref == n.Ref {
			t.Fatalf("node with forged unknown flags matched node with real known flags: %s", n.Ref)
		}
	})

	t.Run("context_and_visible_container_shape_isolation", func(t *testing.T) {
		build := func(asContext bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			box := withClass(device.RawNode{Visible: !asContext, RawBounds: rect(0, 0, 1000, 500), Bounds: rect(0, 0, 1000, 500)}, "android.widget.FrameLayout")
			boxIdx := c.add(root, box)
			c.add(boxIdx, withText(withClass(visible(rect(10, 10, 300, 80)), "android.widget.TextView"), "leaf"))
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build(true))
		second := buildOne(t, b, build(false))
		if first.Tree.Root.Children[0].Ref == second.Tree.Root.Children[0].Ref {
			t.Fatalf("context group matched visible view container despite role change")
		}
	})
}

func TestContinuityLocalConsumptionCannotCreateGlobalUnique(t *testing.T) {
	buildScenario := func(secondFrame bool, useCompatClass bool) *device.Capture {
		c := newCap(1080, 2400)
		root := c.root()

		btnClass := "android.widget.Button"
		if secondFrame && useCompatClass {
			btnClass = "androidx.appcompat.widget.AppCompatButton"
		}

		// cardA has distinct action (LongClickable) so Stage 1 anchors it directly.
		cardA := c.add(root, longClickable(withClass(visible(rect(0, 0, 1080, 300)), "android.widget.FrameLayout")))
		c.add(cardA, clickable(withRes(withClass(withText(visible(rect(10, 10, 300, 90)), "收藏"), btnClass), "app:id/fav")))

		addOther := func(y int) {
			other := c.add(root, withClass(visible(rect(0, y, 1080, y+250)), "android.widget.LinearLayout"))
			c.add(other, withText(withClass(visible(rect(10, y+10, 400, y+80)), "android.widget.TextView"), "OtherTitle"))
		}
		addBC := func(y int, title string) {
			card := c.add(root, withClass(visible(rect(0, y, 1080, y+250)), "android.widget.LinearLayout"))
			c.add(card, withText(withClass(visible(rect(10, y+10, 400, y+80)), "android.widget.TextView"), title))
			c.add(card, clickable(withRes(withClass(withText(visible(rect(500, y+10, 700, y+80)), "收藏"), btnClass), "app:id/fav")))
		}
		if !secondFrame {
			addOther(320)
			addBC(600, "CardB_Only")
		} else {
			// Shifted otherCard rank 0 -> 1 so Stage 5 rejects group, isolating Pass 3/6 + Stage 3.
			addBC(320, "CardC_New")
			addOther(600)
		}
		return c.capture()
	}

	t.Run("global_exact_sees_full_bucket_cardinality", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, buildScenario(false, false))
		second := buildOne(t, b, buildScenario(true, false))

		// cardA's fav button keeps ref via local Pass 2.
		if first.Tree.Root.Children[0].Children[0].Ref != second.Tree.Root.Children[0].Children[0].Ref {
			t.Fatalf("cardA fav button lost ref")
		}
		oldCardB := findCardByTitle(first.Tree, "CardB_Only")
		newCardC := findCardByTitle(second.Tree, "CardC_New")
		if newCardC.Ref == oldCardB.Ref {
			t.Fatalf("cardC reused cardB container ref %s", oldCardB.Ref)
		}
		if newCardC.Children[1].Ref == oldCardB.Children[1].Ref {
			t.Fatalf("cardC fav reused cardB fav ref %s due to local consumption shrinking global bucket",
				oldCardB.Children[1].Ref)
		}
	})

	t.Run("global_public_fallback_sees_full_bucket_cardinality", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, buildScenario(false, true))
		second := buildOne(t, b, buildScenario(true, true))

		// cardA's fav button keeps ref via local Pass 5 (public fallback).
		if first.Tree.Root.Children[0].Children[0].Ref != second.Tree.Root.Children[0].Children[0].Ref {
			t.Fatalf("cardA fav button lost ref in local public fallback")
		}
		oldCardB := findCardByTitle(first.Tree, "CardB_Only")
		newCardC := findCardByTitle(second.Tree, "CardC_New")
		if newCardC.Ref == oldCardB.Ref {
			t.Fatalf("cardC reused cardB container ref %s", oldCardB.Ref)
		}
		if newCardC.Children[1].Ref == oldCardB.Children[1].Ref {
			t.Fatalf("cardC fav reused cardB fav ref %s in global public fallback",
				oldCardB.Children[1].Ref)
		}
	})

	t.Run("local_resource_fallback_control_preserves_unique_local_label_change", func(t *testing.T) {
		build := func(secondFrame bool) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			cardA := c.add(root, longClickable(withClass(visible(rect(0, 0, 1080, 300)), "android.widget.FrameLayout")))
			dynLabel := "计数 1"
			if secondFrame {
				dynLabel = "计数 2"
			}
			c.add(cardA, clickable(withRes(withClass(withText(visible(rect(10, 10, 300, 90)), dynLabel), "android.widget.Button"), "app:id/btn_dyn")))
			c.add(cardA, clickable(withRes(withClass(withText(visible(rect(320, 10, 600, 90)), "确定"), "android.widget.Button"), "app:id/btn_keep")))

			addOther := func(y int) {
				other := c.add(root, withClass(visible(rect(0, y, 1080, y+250)), "android.widget.LinearLayout"))
				c.add(other, withText(withClass(visible(rect(10, y+10, 400, y+80)), "android.widget.TextView"), "OtherTitle"))
			}
			addDynamicCard := func(y int, title, btnLabel string) {
				cardOther := c.add(root, withClass(visible(rect(0, y, 1080, y+250)), "android.widget.LinearLayout"))
				c.add(cardOther, withText(withClass(visible(rect(10, y+10, 300, y+80)), "android.widget.TextView"), title))
				c.add(cardOther, clickable(withRes(withClass(withText(visible(rect(320, y+10, 600, y+80)), btnLabel), "android.widget.Button"), "app:id/btn_dyn")))
			}
			if !secondFrame {
				addOther(320)
				addDynamicCard(600, "RowB", "计数 1")
			} else {
				// Shift addOther across sibling rank so Stage 5 rejects the LinearLayout group and cardOther stays unanchored.
				addDynamicCard(320, "RowC", "计数 3")
				addOther(600)
			}
			return c.capture()
		}

		b := NewBuilder()
		first := buildOne(t, b, build(false))
		second := buildOne(t, b, build(true))
		if first.Tree.Root.Children[0].Children[0].Ref != second.Tree.Root.Children[0].Children[0].Ref {
			t.Fatalf("cardA local resource-only label change lost ref: %s -> %s",
				first.Tree.Root.Children[0].Children[0].Ref, second.Tree.Root.Children[0].Children[0].Ref)
		}
		oldOther := findCardByTitle(first.Tree, "RowB")
		newOther := findCardByTitle(second.Tree, "RowC")
		if oldOther == nil || newOther == nil {
			t.Fatalf("missing RowB or RowC card: %v / %v", oldOther, newOther)
		}
		if oldOther.Children[1].Ref == newOther.Children[1].Ref {
			t.Fatalf("disappeared/new card button wrongly matched globally by resource")
		}
	})
}
