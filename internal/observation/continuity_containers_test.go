package observation

import (
	"fmt"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// templateRowListCapture builds a scroll list of clickable rows that share one
// template resource id (the shape of a search-result list). Every row carries
// only its own unique text children, so the row containers are distinguishable
// exclusively through descendant voting.
func templateRowListCapture(titles []string) *device.Capture {
	c := newCap(1080, 2400)
	root := c.root()
	scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
	for i, title := range titles {
		y := 120 + i*120
		row := c.add(scroll, clickable(withRes(withClass(
			visible(rect(0, y, 1080, y+110)), "android.widget.LinearLayout"), "app:id/search_find")))
		c.add(row, withText(withClass(visible(rect(20, y+10, 700, y+60)), "android.widget.TextView"), title))
		c.add(row, withText(withClass(visible(rect(20, y+60, 700, y+100)), "android.widget.TextView"), title+" 说明"))
	}
	return c.capture()
}

// rowOf returns the row container whose direct children carry the given label.
func rowOf(tree *protocol.Tree, label string) *protocol.Node {
	return findCardByTitle(tree, label)
}

func TestContinuityTemplateResourceRowsKeepRefs(t *testing.T) {
	titles := make([]string, 17)
	for i := range titles {
		titles[i] = fmt.Sprintf("结果 %02d", i)
	}
	b := NewBuilder()
	first := buildOne(t, b, templateRowListCapture(titles))
	second := buildOne(t, b, templateRowListCapture(titles))

	for _, title := range titles {
		oldRow := rowOf(first.Tree, title)
		newRow := rowOf(second.Tree, title)
		if oldRow == nil || newRow == nil {
			t.Fatalf("missing row for %q", title)
		}
		if oldRow.Ref != newRow.Ref {
			t.Fatalf("template-resource row %q renumbered: %s -> %s\n%s\n%s",
				title, oldRow.Ref, newRow.Ref, dump(first.Tree), dump(second.Tree))
		}
		if refOf(first, title) != refOf(second, title) {
			t.Fatalf("text child %q renumbered: %s -> %s", title, refOf(first, title), refOf(second, title))
		}
	}
	if len(first.Refs) != len(second.Refs) {
		t.Fatalf("ref count changed: %d -> %d", len(first.Refs), len(second.Refs))
	}
	for i := range first.Refs {
		if first.Refs[i] != second.Refs[i] {
			t.Fatalf("public preorder ref %d drifted: %s -> %s", i, first.Refs[i], second.Refs[i])
		}
	}

	base, current := ProjectCompactTree(first.Tree), ProjectCompactTree(second.Tree)
	delta, err := ReadDiff(first.Tree.Root.Ref, base, current)
	if err != nil {
		t.Fatalf("identical static list is not representable: %v", err)
	}
	if !delta.IsEmpty() {
		t.Fatalf("static list produced a non-empty delta: set=%d add=%d remove=%d children=%d replace=%d",
			len(delta.Set), len(delta.Add), len(delta.Remove), len(delta.Children), len(delta.Replace))
	}
	if got := b.lastStats.stage3Matched; got != len(titles) {
		t.Fatalf("expected %d rows anchored by descendant vote, got %d", len(titles), got)
	}
	if got := b.lastStats.stage6Matched; got != 0 {
		t.Fatalf("static geometry must not be needed for identical rows, matched %d", got)
	}
}

func TestContinuityTemplateRowsFollowDescendantsOnSwap(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, templateRowListCapture([]string{"甲", "乙"}))
	second := buildOne(t, b, templateRowListCapture([]string{"乙", "甲"}))

	// The text children are unique, so they keep their refs. Each container must
	// follow its descendants, not its slot: the row now holding 乙 keeps 乙's old
	// container ref and the row now holding 甲 keeps 甲's.
	for _, title := range []string{"甲", "乙"} {
		oldRow := rowOf(first.Tree, title)
		newRow := rowOf(second.Tree, title)
		if oldRow.Ref != newRow.Ref {
			t.Fatalf("row for %q did not follow its descendants: %s -> %s\n%s\n%s",
				title, oldRow.Ref, newRow.Ref, dump(first.Tree), dump(second.Tree))
		}
		if refOf(first, title) != refOf(second, title) {
			t.Fatalf("text %q renumbered", title)
		}
	}
	if rowOf(first.Tree, "甲").Ref == rowOf(first.Tree, "乙").Ref {
		t.Fatal("test premise broken: rows share a container ref")
	}
}

// rebuiltWrapperListCapture builds the same list with an extra outer wrapper in
// the second frame. The wrapper is a new container between root and the scroll,
// so the scroll's parent is not anchored and only bottom-up voting can recover
// the list and its cards.
func rebuiltWrapperListCapture(wrapped bool) *device.Capture {
	c := newCap(1080, 2400)
	root := c.root()
	parent := root
	if wrapped {
		parent = c.add(root, withClass(visible(rect(0, 90, 1080, 2340)), "android.widget.FrameLayout"))
	}
	scroll := c.add(parent, scrollable(withClass(visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
	for idx, title := range []string{"卡片甲", "卡片乙", "卡片丙"} {
		y := 120 + idx*380
		card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+340)), "android.widget.LinearLayout"))
		c.add(card, withText(withClass(visible(rect(20, y+20, 700, y+120)), "android.widget.TextView"), title))
		for b := 0; b < 2; b++ {
			bx := 820 + b*110
			btn := clickable(withRes(withClass(withText(visible(rect(bx, y+20, bx+100, y+120)), "赞"), "android.widget.Button"), "app:id/like"))
			c.add(card, btn)
		}
	}
	return c.capture()
}

func TestContinuityBottomUpAnchorsAcrossRebuiltWrapper(t *testing.T) {
	t.Run("inserted_wrapper_layer", func(t *testing.T) {
		b := NewBuilder()
		first := buildOne(t, b, rebuiltWrapperListCapture(false))
		second := buildOne(t, b, rebuiltWrapperListCapture(true))
		assertBottomUpRecovery(t, b, first, second)
	})

	t.Run("changed_wrapper_resource", func(t *testing.T) {
		build := func(resource string) *device.Capture {
			c := newCap(1080, 2400)
			root := c.root()
			wrapper := c.add(root, clickable(withRes(withClass(
				visible(rect(0, 90, 1080, 2340)), "android.widget.LinearLayout"), resource)))
			scroll := c.add(wrapper, scrollable(withClass(visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
			for idx, title := range []string{"卡片甲", "卡片乙", "卡片丙"} {
				y := 120 + idx*380
				card := c.add(scroll, withClass(visible(rect(0, y, 1080, y+340)), "android.widget.LinearLayout"))
				c.add(card, withText(withClass(visible(rect(20, y+20, 700, y+120)), "android.widget.TextView"), title))
				btn := clickable(withRes(withClass(withText(visible(rect(820, y+20, 920, y+120)), "赞"), "android.widget.Button"), "app:id/like"))
				c.add(card, btn)
			}
			return c.capture()
		}
		b := NewBuilder()
		first := buildOne(t, b, build("app:id/shell_v1"))
		second := buildOne(t, b, build("app:id/shell_v2"))
		shell := second.Tree.Root.Children[0]
		if shell.Resource == nil || *shell.Resource != "app:id/shell_v2" {
			t.Fatalf("test premise broken: wrapper resource = %v", shell.Resource)
		}
		oldShell := first.Tree.Root.Children[0]
		if shell.Ref == oldShell.Ref {
			t.Fatalf("wrapper with a changed resource kept ref %s", shell.Ref)
		}
		oldScroll := oldShell.Children[0]
		newScroll := shell.Children[0]
		if oldScroll.Ref != newScroll.Ref {
			t.Fatalf("list across a re-identified wrapper lost ref: %s -> %s\n%s",
				oldScroll.Ref, newScroll.Ref, dump(second.Tree))
		}
		for _, title := range []string{"卡片甲", "卡片乙", "卡片丙"} {
			oldCard := findCardByTitle(first.Tree, title)
			newCard := findCardByTitle(second.Tree, title)
			if oldCard.Ref != newCard.Ref || oldCard.Children[0].Ref != newCard.Children[0].Ref {
				t.Fatalf("card %q across a re-identified wrapper lost ref", title)
			}
			if oldCard.Children[1].Ref != newCard.Children[1].Ref {
				t.Fatalf("card %q button across a re-identified wrapper lost ref", title)
			}
		}
		if got := b.lastStats.stage6Matched; got != 0 {
			t.Fatalf("single buttons must be recovered by local recovery, stage6Matched = %d", got)
		}
	})
}

// assertBottomUpRecovery checks the list, its cards and their children survived
// an outer wrapper rebuild.
func assertBottomUpRecovery(t *testing.T, b *Builder, first, second *Snapshot) {
	t.Helper()
	if len(second.Tree.Root.Children) != 1 || len(second.Tree.Root.Children[0].Children) == 0 {
		t.Fatalf("unexpected second tree shape:\n%s", dump(second.Tree))
	}
	newScroll := second.Tree.Root.Children[0].Children[0]
	oldScroll := first.Tree.Root.Children[0]
	if newScroll.Ref != oldScroll.Ref {
		t.Fatalf("list across rebuilt wrapper did not keep ref: %s -> %s\n%s",
			oldScroll.Ref, newScroll.Ref, dump(second.Tree))
	}
	for _, title := range []string{"卡片甲", "卡片乙", "卡片丙"} {
		oldCard := findCardByTitle(first.Tree, title)
		newCard := findCardByTitle(second.Tree, title)
		if oldCard == nil || newCard == nil {
			t.Fatalf("missing card %q", title)
		}
		if oldCard.Ref != newCard.Ref {
			t.Fatalf("card %q across rebuilt wrapper lost ref: %s -> %s\n%s",
				title, oldCard.Ref, newCard.Ref, dump(second.Tree))
		}
		if oldCard.Children[0].Ref != newCard.Children[0].Ref {
			t.Fatalf("card %q title lost ref", title)
		}
		for i := 1; i < len(oldCard.Children); i++ {
			if oldCard.Children[i].Ref != newCard.Children[i].Ref {
				t.Fatalf("card %q duplicate button %d lost ref: %s -> %s",
					title, i, oldCard.Children[i].Ref, newCard.Children[i].Ref)
			}
		}
	}
	if got := b.lastStats.stage3Matched; got != 4 {
		t.Fatalf("expected the bottom-up vote to anchor the list and 3 cards, got %d", got)
	}
	if got := b.lastStats.stage6Matched; got != 6 {
		t.Fatalf("expected 6 static duplicate buttons, got %d", got)
	}
}

// templateOrdinalRowCapture builds three clickable template-resource rows. Only
// the middle row survives a content replacement in the second frame, so it is
// the independent fixed point of the generalized ordinal bucket.
func templateOrdinalRowCapture(titles []string) *device.Capture {
	c := newCap(1080, 2400)
	root := c.root()
	scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
	for idx, title := range titles {
		y := 120 + idx*200
		row := c.add(scroll, clickable(withRes(withClass(
			visible(rect(0, y, 1080, y+180)), "android.widget.LinearLayout"), "app:id/result_row")))
		c.add(row, withText(withClass(visible(rect(20, y+20, 700, y+100)), "android.widget.TextView"), title))
	}
	return c.capture()
}

func TestContinuityOrdinalGeneralizedContainerBucket(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, templateOrdinalRowCapture([]string{"旧一", "固定", "旧三"}))
	second := buildOne(t, b, templateOrdinalRowCapture([]string{"新一", "固定", "新三"}))

	// The unchanged middle row anchors the ordinal bucket; the two replaced
	// template rows keep their container refs at the same rank while their
	// changed text children get fresh refs.
	for _, pair := range [][2]string{{"旧一", "新一"}, {"固定", "固定"}, {"旧三", "新三"}} {
		oldRow := rowOf(first.Tree, pair[0])
		newRow := rowOf(second.Tree, pair[1])
		if oldRow == nil || newRow == nil {
			t.Fatalf("missing row %q/%q", pair[0], pair[1])
		}
		if oldRow.Ref != newRow.Ref {
			t.Fatalf("row %q -> %q lost container ref: %s -> %s\n%s\n%s",
				pair[0], pair[1], oldRow.Ref, newRow.Ref, dump(first.Tree), dump(second.Tree))
		}
	}
	if refOf(first, "旧一") == refOf(second, "新一") {
		t.Fatalf("replaced text reused old ref %s", refOf(first, "旧一"))
	}
	if refOf(first, "旧三") == refOf(second, "新三") {
		t.Fatalf("replaced text reused old ref %s", refOf(first, "旧三"))
	}
	if refOf(first, "固定") != refOf(second, "固定") {
		t.Fatalf("fixed row text lost ref")
	}
	if got := b.lastStats.stage5Matched; got != 2 {
		t.Fatalf("expected the generalized ordinal pass to recover 2 rows, got %d", got)
	}
}

func TestContinuityOrdinalGeneralizedBucketRequiresEqualCounts(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, templateOrdinalRowCapture([]string{"旧一", "固定"}))
	second := buildOne(t, b, templateOrdinalRowCapture([]string{"新一", "固定", "新三"}))

	oldFixed := rowOf(first.Tree, "固定")
	newFixed := rowOf(second.Tree, "固定")
	if oldFixed.Ref != newFixed.Ref {
		t.Fatalf("fixed row lost ref when a template row was added: %s -> %s",
			oldFixed.Ref, newFixed.Ref)
	}
	oldRows := map[string]bool{}
	for _, label := range []string{"旧一", "固定"} {
		oldRows[rowOf(first.Tree, label).Ref] = true
	}
	for _, label := range []string{"新一", "新三"} {
		row := rowOf(second.Tree, label)
		if oldRows[row.Ref] {
			t.Fatalf("unequal row counts still allowed ordinal reuse of %s for %q", row.Ref, label)
		}
	}
}

func TestContinuityNewDuplicateTemplateRowDoesNotStealRef(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, templateRowListCapture([]string{"甲", "乙"}))
	oldRefs := map[string]bool{}
	for _, label := range []string{"甲", "乙"} {
		oldRefs[rowOf(first.Tree, label).Ref] = true
		oldRefs[refOf(first, label)] = true
	}
	second := buildOne(t, b, templateRowListCapture([]string{"甲", "乙", "丙"}))

	for _, label := range []string{"甲", "乙"} {
		if rowOf(first.Tree, label).Ref != rowOf(second.Tree, label).Ref {
			t.Fatalf("surviving template row %q lost ref", label)
		}
	}
	newRow := rowOf(second.Tree, "丙")
	if newRow == nil {
		t.Fatalf("missing new row:\n%s", dump(second.Tree))
	}
	if oldRefs[newRow.Ref] || oldRefs[refOf(second, "丙")] {
		t.Fatalf("new duplicate template row reused old ref %s / %s", newRow.Ref, refOf(second, "丙"))
	}
}
