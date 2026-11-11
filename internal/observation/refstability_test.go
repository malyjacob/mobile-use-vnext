package observation

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// These tests pin the public ref-stability contract: a ref may change only
// when the public facts of the node (role, label, published resource, actions,
// protection) or its structural position change. Raw attributes that are not
// published must not, on their own, force a new ref.

// contextWrapperCapture builds root -> invisible wrapper -> visible text. The
// wrapper is projected as a context group; it has no Source entry, only a
// ContextSource entry after the fix.
func contextWrapperCapture() *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	wrapper := c.add(root, withPkg(withClass(device.RawNode{
		Visible:   false,
		RawBounds: rect(0, 0, 1000, 1000),
		Bounds:    rect(0, 0, 1000, 1000),
	}, "android.widget.FrameLayout"), "com.example"))
	c.add(wrapper, withText(withClass(visible(rect(10, 10, 500, 80)), "android.widget.TextView"), "hello"))
	return c
}

func TestContinuityContextWrapperKeepsRef(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, contextWrapperCapture().capture())
	second := buildOne(t, b, contextWrapperCapture().capture())

	firstWrapper := first.Tree.Root.Children[0]
	secondWrapper := second.Tree.Root.Children[0]
	if !firstWrapper.Context || !secondWrapper.Context {
		t.Fatalf("expected context wrappers:\n%s\n%s", dump(first.Tree), dump(second.Tree))
	}
	if firstWrapper.Ref != secondWrapper.Ref {
		t.Fatalf("context wrapper renumbered: %s -> %s\n%s\n%s",
			firstWrapper.Ref, secondWrapper.Ref, dump(first.Tree), dump(second.Tree))
	}
	delta, err := Diff(first.Tree.Root.Ref, first.Tree, second.Tree)
	if err != nil {
		t.Fatalf("identical tree is not representable: %v\n%s\n%s", err, dump(first.Tree), dump(second.Tree))
	}
	if !delta.IsEmpty() {
		t.Fatalf("identical tree produced a non-empty delta: %+v", delta)
	}
}

// hiddenDuplicateCapture builds a hidden text node before the visible one.
// The hidden node is not published, so it must neither receive a ref nor
// consume the visible node's previous ref.
func hiddenDuplicateCapture() *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	c.add(root, withText(withClass(device.RawNode{
		Visible:   false,
		RawBounds: rect(0, 0, 300, 80),
		Bounds:    rect(0, 0, 300, 80),
	}, "android.widget.TextView"), "设置"))
	c.add(root, withText(withClass(visible(rect(0, 100, 300, 180)), "android.widget.TextView"), "设置"))
	return c
}

func TestContinuityHiddenDuplicateDoesNotConsumeRef(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, hiddenDuplicateCapture().capture())
	second := buildOne(t, b, hiddenDuplicateCapture().capture())

	if len(first.Tree.Root.Children) != 1 || len(second.Tree.Root.Children) != 1 {
		t.Fatalf("hidden duplicate was published:\n%s\n%s", dump(first.Tree), dump(second.Tree))
	}
	firstRef := first.Tree.Root.Children[0].Ref
	secondRef := second.Tree.Root.Children[0].Ref
	if firstRef != secondRef {
		t.Fatalf("hidden duplicate consumed the visible ref: %s -> %s\n%s\n%s",
			firstRef, secondRef, dump(first.Tree), dump(second.Tree))
	}
	delta, err := Diff(first.Tree.Root.Ref, first.Tree, second.Tree)
	if err != nil {
		t.Fatalf("identical tree is not representable: %v", err)
	}
	if !delta.IsEmpty() {
		t.Fatalf("identical tree produced a non-empty delta: %+v", delta)
	}
}

// anonymousContainerCapture builds root -> anonymous container -> text. The
// container carries no label, action or published resource of its own.
func anonymousContainerCapture(class string) *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	container := c.add(root, withPkg(withClass(visible(rect(0, 0, 1000, 500)), class), "com.example"))
	c.add(container, withText(withClass(visible(rect(10, 10, 500, 80)), "android.widget.TextView"), "hello"))
	return c
}

func TestContinuityAnonymousContainerClassChangeKeepsRef(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, anonymousContainerCapture("android.widget.LinearLayout").capture())
	second := buildOne(t, b, anonymousContainerCapture("android.widget.FrameLayout").capture())

	firstContainer := first.Tree.Root.Children[0]
	secondContainer := second.Tree.Root.Children[0]
	if firstContainer.Ref != secondContainer.Ref {
		t.Fatalf("anonymous container renumbered on hidden class change: %s -> %s\n%s\n%s",
			firstContainer.Ref, secondContainer.Ref, dump(first.Tree), dump(second.Tree))
	}
	delta, err := Diff(first.Tree.Root.Ref, first.Tree, second.Tree)
	if err != nil {
		t.Fatalf("publicly identical tree is not representable: %v\n%s\n%s", err, dump(first.Tree), dump(second.Tree))
	}
	if !delta.IsEmpty() {
		t.Fatalf("publicly identical tree produced a non-empty delta: %+v", delta)
	}
}

// rowsWithHiddenSiblingCapture builds a scroll with two visible rows and one
// unpublished row. The unpublished row must not make the visible rows look
// like an ambiguous sibling list.
func rowsWithHiddenSiblingCapture() *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	scroll := c.add(root, scrollable(withClass(visible(rect(0, 100, 1080, 2000)), "android.widget.ScrollView")))
	for i, label := range []string{"one", "two"} {
		row := c.add(scroll, clickable(withClass(visible(rect(0, 120+i*400, 1080, 500+i*400)), "android.widget.LinearLayout")))
		c.add(row, withText(withClass(visible(rect(20, 140+i*400, 500, 200+i*400)), "android.widget.TextView"), label))
	}
	c.add(scroll, clickable(withClass(device.RawNode{
		Visible:   false,
		RawBounds: rect(0, 900, 1080, 1000),
		Bounds:    rect(0, 900, 1080, 1000),
	}, "android.widget.LinearLayout")))
	return c
}

func TestContinuityHiddenSiblingDoesNotBreakRowRefs(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, rowsWithHiddenSiblingCapture().capture())
	second := buildOne(t, b, rowsWithHiddenSiblingCapture().capture())

	firstRows := first.Tree.Root.Children[0].Children
	secondRows := second.Tree.Root.Children[0].Children
	if len(firstRows) != 2 || len(secondRows) != 2 {
		t.Fatalf("hidden row was published: %d/%d\n%s\n%s", len(firstRows), len(secondRows), dump(first.Tree), dump(second.Tree))
	}
	for i := range firstRows {
		if firstRows[i].Ref != secondRows[i].Ref {
			t.Fatalf("row %d renumbered: %s -> %s\n%s\n%s",
				i, firstRows[i].Ref, secondRows[i].Ref, dump(first.Tree), dump(second.Tree))
		}
	}
	delta, err := Diff(first.Tree.Root.Ref, first.Tree, second.Tree)
	if err != nil {
		t.Fatalf("identical tree is not representable: %v\n%s\n%s", err, dump(first.Tree), dump(second.Tree))
	}
	if !delta.IsEmpty() {
		t.Fatalf("identical tree produced a non-empty delta: %+v", delta)
	}
}

// rowsUnderIdentifiedScrollCapture puts the rows under a scroll that carries
// its own resource id. The scroll is therefore matched by the identity rule,
// not by the container rule; its anonymous rows must still be matched after
// that parent correspondence exists.
func rowsUnderIdentifiedScrollCapture() *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	scroll := c.add(root, clickable(scrollable(withRes(withClass(visible(rect(0, 100, 1080, 2000)), "android.widget.ScrollView"), "app:id/list"))))
	for i, label := range []string{"one", "two"} {
		row := c.add(scroll, clickable(withClass(visible(rect(0, 120+i*400, 1080, 500+i*400)), "android.widget.LinearLayout")))
		c.add(row, withText(withClass(visible(rect(20, 140+i*400, 500, 200+i*400)), "android.widget.TextView"), label))
	}
	return c
}

func TestContinuityRowsUnderIdentifiedScrollKeepRefs(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, rowsUnderIdentifiedScrollCapture().capture())
	second := buildOne(t, b, rowsUnderIdentifiedScrollCapture().capture())

	firstRows := first.Tree.Root.Children[0].Children
	secondRows := second.Tree.Root.Children[0].Children
	if len(firstRows) != 2 || len(secondRows) != 2 {
		t.Fatalf("rows = %d/%d\n%s\n%s", len(firstRows), len(secondRows), dump(first.Tree), dump(second.Tree))
	}
	for i := range firstRows {
		if firstRows[i].Ref != secondRows[i].Ref {
			t.Fatalf("row %d renumbered: %s -> %s\n%s\n%s",
				i, firstRows[i].Ref, secondRows[i].Ref, dump(first.Tree), dump(second.Tree))
		}
	}
	delta, err := Diff(first.Tree.Root.Ref, first.Tree, second.Tree)
	if err != nil {
		t.Fatalf("identical tree is not representable: %v\n%s\n%s", err, dump(first.Tree), dump(second.Tree))
	}
	if !delta.IsEmpty() {
		t.Fatalf("identical tree produced a non-empty delta: %+v", delta)
	}
}

// anonymousResourceCapture builds an unlabelled, non-interactive image whose
// raw resource id changes. The projection does not publish that resource id
// (only unlabelled interactive units get a resource hint), so the public tree
// sees the same anonymous container both times.
func anonymousResourceCapture(class, resource string) *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	c.add(root, enabled(withRes(withClass(visible(rect(0, 0, 120, 120)), class), resource), true))
	return c
}

func TestContinuityUnpublishedResourceDoesNotForceNewRef(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, anonymousResourceCapture("android.widget.ImageView", "app:id/icon").capture())
	second := buildOne(t, b, anonymousResourceCapture("androidx.appcompat.widget.AppCompatImageView", "app:id/icon_v2").capture())

	firstNode := first.Tree.Root.Children[0]
	secondNode := second.Tree.Root.Children[0]
	if firstNode.Resource != nil || secondNode.Resource != nil {
		t.Fatalf("test premise broken: resource should not be published: %v/%v", firstNode.Resource, secondNode.Resource)
	}
	if firstNode.Ref != secondNode.Ref {
		t.Fatalf("anonymous image renumbered on unpublished resource change: %s -> %s\n%s\n%s",
			firstNode.Ref, secondNode.Ref, dump(first.Tree), dump(second.Tree))
	}
	delta, err := Diff(first.Tree.Root.Ref, first.Tree, second.Tree)
	if err != nil {
		t.Fatalf("publicly identical tree is not representable: %v", err)
	}
	if !delta.IsEmpty() {
		t.Fatalf("publicly identical tree produced a non-empty delta: %+v", delta)
	}
}

// duplicateButtonCapture builds a parent with count identical buttons.
func duplicateButtonCapture(count int) *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	bar := c.add(root, withClass(visible(rect(0, 0, 1080, 100)), "android.widget.LinearLayout"))
	for i := 0; i < count; i++ {
		btn := clickable(withRes(withClass(visible(rect(i*100, 0, i*100+90, 90)), "android.widget.Button"), "app:id/action"))
		c.add(bar, withText(btn, "ok"))
	}
	return c
}

func TestContinuityNewDuplicateDoesNotStealRef(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, duplicateButtonCapture(1).capture())
	oldRef := first.Tree.Root.Children[0].Children[0].Ref
	second := buildOne(t, b, duplicateButtonCapture(2).capture())
	buttons := second.Tree.Root.Children[0].Children
	if len(buttons) != 2 {
		t.Fatalf("buttons = %d\n%s", len(buttons), dump(second.Tree))
	}
	for _, button := range buttons {
		if button.Ref == oldRef {
			t.Fatalf("ambiguous current duplicate reused the single previous ref %s\n%s", oldRef, dump(second.Tree))
		}
	}
	if buttons[0].Ref == buttons[1].Ref {
		t.Fatalf("duplicate refs %s", buttons[0].Ref)
	}
}

// labeledClassCapture builds one labeled text unit whose raw class changes
// between observations while every public field stays the same.
func labeledClassCapture(class string) *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	c.add(root, withText(withClass(visible(rect(0, 0, 300, 80)), class), "WLAN"))
	return c
}

func TestContinuityLabeledClassChangeKeepsRef(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, labeledClassCapture("android.widget.TextView").capture())
	second := buildOne(t, b, labeledClassCapture("androidx.appcompat.widget.AppCompatTextView").capture())

	if first.Tree.Root.Children[0].Ref != second.Tree.Root.Children[0].Ref {
		t.Fatalf("labeled node renumbered on hidden class change: %s -> %s\n%s\n%s",
			first.Tree.Root.Children[0].Ref, second.Tree.Root.Children[0].Ref, dump(first.Tree), dump(second.Tree))
	}
	delta, err := Diff(first.Tree.Root.Ref, first.Tree, second.Tree)
	if err != nil {
		t.Fatalf("publicly identical tree is not representable: %v", err)
	}
	if !delta.IsEmpty() {
		t.Fatalf("publicly identical tree produced a non-empty delta: %+v", delta)
	}
}
