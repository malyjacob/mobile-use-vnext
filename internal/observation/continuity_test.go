package observation

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// twoCards builds a scroll container with two card groups, each holding a text
// and a button, plus a search input at the top. This is the shape used by the
// continuity tests.
func twoCards() *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	c.add(root, editable(withRes(withDesc(withClass(visible(rect(0, 0, 1080, 80)), "android.widget.EditText"), "搜索"), "app:id/search")))
	scroll := c.add(root, scrollable(visible(rect(0, 100, 1080, 2300))))
	for _, card := range []struct {
		title string
		y     int
	}{{"帖子甲", 120}, {"帖子乙", 620}} {
		group := c.add(scroll, withClass(visible(rect(0, card.y, 1080, card.y+400)), "android.widget.LinearLayout"))
		c.add(group, withText(withClass(visible(rect(20, card.y+20, 1000, card.y+200)), "android.widget.TextView"), card.title))
		c.add(group, clickable(withRes(withClass(withText(visible(rect(900, card.y+250, 1060, card.y+330)), "收藏"), "android.widget.Button"), "app:id/fav")))
	}
	return c
}

// oldCardRef returns the ref of the card group whose first child has label.
func oldCardRef(snap *Snapshot, label string) string {
	for _, ref := range snap.Refs {
		n := find(snap.Tree, ref)
		if len(n.Children) == 2 && len(n.Children[0].Label) > 0 && n.Children[0].Label[0] == label {
			return ref
		}
	}
	return ""
}

func refOf(snap *Snapshot, label string) string {
	for _, ref := range snap.Refs {
		n := find(snap.Tree, ref)
		if len(n.Label) > 0 && n.Label[0] == label {
			return ref
		}
	}
	return ""
}

func TestContinuityUnchangedTreeKeepsRefs(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, twoCards().capture())
	second := buildOne(t, b, twoCards().capture())
	if len(first.Refs) != len(second.Refs) {
		t.Fatalf("ref count changed: %d -> %d", len(first.Refs), len(second.Refs))
	}
	for i := range first.Refs {
		if first.Refs[i] != second.Refs[i] {
			t.Fatalf("unchanged tree renumbered at %d: %s -> %s", i, first.Refs[i], second.Refs[i])
		}
	}
	if dump(first.Tree) != dump(second.Tree) {
		t.Fatalf("trees differ:\n%s\n%s", dump(first.Tree), dump(second.Tree))
	}
}

func TestContinuityInputValueChangeKeepsRef(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, twoCards().capture())
	searchRef := refOf(first, "搜索")
	if searchRef == "" {
		t.Fatalf("search input not found:\n%s", dump(first.Tree))
	}

	c := twoCards()
	c.nodes[1].Text, c.nodes[1].TextSet = "测试", true
	second := buildOne(t, b, c.capture())
	if got := second.Tree.Root.Children[0].Ref; got != searchRef {
		t.Fatalf("input value change broke identity: %s -> %s\n%s", searchRef, got, dump(second.Tree))
	}
	node := find(second.Tree, searchRef)
	if node.Value == nil || *node.Value != "测试" {
		t.Fatalf("value = %v", node.Value)
	}
}

func TestContinuityInputValueChangeWithoutAnchorKeepsRef(t *testing.T) {
	// An input with no resource id and no description is still the same input
	// when its text changes, as long as it is the only input under its parent.
	build := func(text string) *capBuilder {
		c := newCap(1080, 2400)
		root := c.root()
		c.add(root, withText(editable(withClass(visible(rect(0, 0, 1080, 80)), "android.widget.EditText")), text))
		return c
	}
	b := NewBuilder()
	first := buildOne(t, b, build("").capture())
	second := buildOne(t, b, build("hello").capture())
	if first.Tree.Root.Children[0].Ref != second.Tree.Root.Children[0].Ref {
		t.Fatalf("input renumbered on value change: %s -> %s",
			first.Tree.Root.Children[0].Ref, second.Tree.Root.Children[0].Ref)
	}
}

func TestContinuityReorderedSiblingsKeepRefs(t *testing.T) {
	newOrder := func(reversed bool) *device.Capture {
		c := newCap(1080, 2400)
		root := c.root()
		// The raw helper always appends in call order, so build in the desired
		// order and compare refs by label.
		names := []string{"a", "b", "c"}
		if reversed {
			names = []string{"c", "a", "b"}
		}
		for i, name := range names {
			c.add(root, withText(visible(rect(0, i*100, 100, i*100+90)), name))
		}
		return c.capture()
	}
	b := NewBuilder()
	first := buildOne(t, b, newOrder(false))
	before := map[string]string{}
	for _, child := range first.Tree.Root.Children {
		before[child.Label[0]] = child.Ref
	}
	second := buildOne(t, b, newOrder(true))
	for _, child := range second.Tree.Root.Children {
		if before[child.Label[0]] != child.Ref {
			t.Fatalf("reordered sibling %q changed ref %s -> %s\n%s",
				child.Label[0], before[child.Label[0]], child.Ref, dump(second.Tree))
		}
	}
}

func TestContinuityReplacedNodeGetsNewRef(t *testing.T) {
	build := func(title string) *capBuilder {
		c := twoCards()
		c.nodes[4].Text = title // first card title
		return c
	}
	b := NewBuilder()
	first := buildOne(t, b, build("帖子甲").capture())
	oldRef := refOf(first, "帖子甲")
	second := buildOne(t, b, build("帖子丙").capture())
	newRef := refOf(second, "帖子丙")
	if newRef == "" {
		t.Fatalf("new text not found:\n%s", dump(second.Tree))
	}
	if newRef == oldRef {
		t.Fatalf("a different content unit reused ref %s", oldRef)
	}
	// The two "收藏" buttons share label, class, package and resource, but
	// each has exactly one previous candidate under its own matched parent, so
	// parent context disambiguates them and both keep their refs.
	firstCard := find(first.Tree, oldCardRef(first, "帖子甲"))
	secondCard := find(second.Tree, oldCardRef(second, "帖子丙"))
	if firstCard == nil || secondCard == nil {
		t.Fatalf("card lookup failed")
	}
	if len(firstCard.Children) != 2 || len(secondCard.Children) != 2 {
		t.Fatalf("card children = %d/%d", len(firstCard.Children), len(secondCard.Children))
	}
	if got, want := secondCard.Children[1].Ref, firstCard.Children[1].Ref; got != want {
		t.Fatalf("sibling button changed ref: %s -> %s", want, got)
	}
}

func TestContinuityAmbiguousDuplicatesGetNewRefs(t *testing.T) {
	build := func(labels [2]string) *capBuilder {
		// Two structurally identical buttons under one parent: same class,
		// package, resource, label and rectangle. The source offers no other
		// evidence (not even a separating geometry), so both sides stay
		// ambiguous and neither may steal the other's ref.
		c := newCap(1080, 2400)
		root := c.root()
		bar := c.add(root, withClass(visible(rect(0, 0, 1080, 100)), "android.widget.LinearLayout"))
		for _, label := range labels {
			btn := clickable(withRes(withClass(visible(rect(0, 0, 90, 90)), "android.widget.Button"), "app:id/action"))
			c.add(bar, withText(btn, label))
		}
		return c
	}
	b := NewBuilder()
	first := buildOne(t, b, build([2]string{"ok", "ok"}).capture())
	bar := first.Tree.Root.Children[0]
	if len(bar.Children) != 2 {
		t.Fatalf("bar children = %d", len(bar.Children))
	}
	firstRefs := []string{bar.Children[0].Ref, bar.Children[1].Ref}

	// Same situation again: two equally plausible previous candidates exist.
	// Neither current node may steal one; both get fresh refs.
	second := buildOne(t, b, build([2]string{"ok", "ok"}).capture())
	bar2 := second.Tree.Root.Children[0]
	used := map[string]bool{firstRefs[0]: true, firstRefs[1]: true}
	for _, child := range bar2.Children {
		if used[child.Ref] {
			t.Fatalf("ambiguous node was matched to %s; must allocate a new ref", child.Ref)
		}
	}
	if bar2.Children[0].Ref == bar2.Children[1].Ref {
		t.Fatalf("duplicate refs %s", bar2.Children[0].Ref)
	}
}

func TestContinuityUniqueGlobalMatchAcrossReparenting(t *testing.T) {
	// A uniquely identifiable node that appears in a new position (same
	// parent chain but a new sibling order) keeps its ref through rule 2.
	build := func(extra bool) *capBuilder {
		c := newCap(1080, 2400)
		root := c.root()
		left := c.add(root, withClass(visible(rect(0, 0, 500, 2400)), "android.widget.LinearLayout"))
		c.add(left, withText(withRes(withClass(visible(rect(0, 0, 500, 90)), "android.widget.TextView"), "app:id/title"), "固定标题"))
		if extra {
			c.add(root, withText(visible(rect(600, 0, 1000, 90)), "临时"))
		}
		return c
	}
	b := NewBuilder()
	first := buildOne(t, b, build(false).capture())
	titleRef := refOf(first, "固定标题")
	second := buildOne(t, b, build(true).capture())
	if got := refOf(second, "固定标题"); got != titleRef {
		t.Fatalf("unique node lost identity: %s -> %s", titleRef, got)
	}
	// The extra node is new and must not reuse the title's ref.
	if refOf(second, "临时") == titleRef {
		t.Fatalf("new node reused a live ref")
	}
}

func TestContinuityRoleChangeGetsNewRef(t *testing.T) {
	build := func(click bool) *capBuilder {
		c := newCap(1080, 2400)
		root := c.root()
		raw := withText(withRes(withClass(visible(rect(0, 0, 200, 90)), "android.widget.TextView"), "app:id/item"), "条目")
		if click {
			raw.HasClickable, raw.Clickable = true, true
		}
		c.add(root, raw)
		return c
	}
	b := NewBuilder()
	first := buildOne(t, b, build(false).capture())
	firstRef := first.Tree.Root.Children[0].Ref
	second := buildOne(t, b, build(true).capture())
	secondRef := second.Tree.Root.Children[0].Ref
	if firstRef == secondRef {
		t.Fatalf("role change kept ref %s", firstRef)
	}
}

func TestContinuityReappearingNodeGetsNewRef(t *testing.T) {
	build := func(present bool) *capBuilder {
		c := newCap(1080, 2400)
		root := c.root()
		if present {
			c.add(root, withText(withRes(withClass(visible(rect(0, 0, 200, 90)), "android.widget.TextView"), "app:id/banner"), "提示"))
		}
		c.add(root, withText(visible(rect(0, 200, 200, 290)), "固定"))
		return c
	}
	b := NewBuilder()
	first := buildOne(t, b, build(true).capture())
	bannerRef := refOf(first, "提示")
	_ = buildOne(t, b, build(false).capture())
	third := buildOne(t, b, build(true).capture())
	if got := refOf(third, "提示"); got == bannerRef {
		t.Fatalf("a ref that left the tree was resurrected (%s)", bannerRef)
	}
	if refOf(third, "固定") == "" {
		t.Fatalf("stable node missing")
	}
}

func TestContinuitySyntheticRootPersists(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, twoCards().capture())
	c := newCap(1080, 2400) // completely different tree
	root := c.root()
	c.add(root, withText(visible(rect(0, 0, 100, 50)), "别的页面"))
	second := buildOne(t, b, c.capture())
	if first.Tree.Root.Ref != second.Tree.Root.Ref {
		t.Fatalf("synthetic root renumbered: %s -> %s", first.Tree.Root.Ref, second.Tree.Root.Ref)
	}
	if second.Tree.Root.Children[0].Ref == first.Tree.Root.Children[1].Ref {
		t.Fatalf("new application node reused a live ref")
	}
}
