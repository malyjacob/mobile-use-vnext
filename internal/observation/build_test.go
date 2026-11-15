package observation

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestBuildRejectsUnusableCapture(t *testing.T) {
	b := NewBuilder()
	cases := []struct {
		name string
		cap  *device.Capture
	}{
		{"nil", nil},
		{"empty display", &device.Capture{Display: device.Display{Width: 0, Height: 2400}, Nodes: []device.RawNode{{Parent: -1}}}},
		{"zero height", &device.Capture{Display: device.Display{Width: 1080}, Nodes: []device.RawNode{{Parent: -1}}}},
		{"root out of range", &device.Capture{Display: device.Display{Width: 1080, Height: 2400}, Nodes: []device.RawNode{}}},
		{"root has parent", &device.Capture{
			Display: device.Display{Width: 1080, Height: 2400},
			Nodes:   []device.RawNode{{Parent: 1}, {Parent: -1}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if snap, err := b.Build(tc.cap); err == nil || snap != nil {
				t.Fatalf("Build = %v, %v; want rejection", snap, err)
			}
			if b.Previous() != nil {
				t.Fatalf("rejected capture must not be published")
			}
		})
	}
}

func TestBuildSyntheticRoot(t *testing.T) {
	c := newCap(1080, 2400)
	c.root()
	snap := buildOne(t, NewBuilder(), c.capture())

	root := snap.Tree.Root
	if snap.Tree.Format != "full" || snap.Tree.Scope != protocol.Scope {
		t.Fatalf("tree header = %q/%q", snap.Tree.Format, snap.Tree.Scope)
	}
	if root.Role != protocol.RoleRoot || root.Ref != "n1" {
		t.Fatalf("root = %s %s", root.Ref, root.Role)
	}
	want := protocol.Bounds{Left: 0, Top: 0, Right: 1080, Bottom: 2400}
	if root.Bounds == nil || *root.Bounds != want {
		t.Fatalf("root bounds = %v", root.Bounds)
	}
	if root.Enabled == nil || !*root.Enabled {
		t.Fatalf("root enabled must be known true")
	}
	for name, v := range map[string]*bool{"focused": root.Focused, "selected": root.Selected, "protected": root.Protected, "checked": root.Checked} {
		if v == nil || *v {
			t.Fatalf("root %s must be known false", name)
		}
	}
	if len(root.Actions) != 0 || len(root.Children) != 0 || len(root.Label) != 0 {
		t.Fatalf("root must have no actions, label or children: %+v", root)
	}
	if len(snap.Refs) != 1 || snap.Refs[0] != "n1" {
		t.Fatalf("refs = %v", snap.Refs)
	}
	if idx, ok := snap.Source["n1"]; !ok || idx != 0 {
		t.Fatalf("root source = %d, %t", idx, ok)
	}
}

func TestBuildVisibleFilteringAndContextAncestor(t *testing.T) {
	c := newCap(1080, 2400)
	root := c.root()
	// Visible layout shell with no content of its own but a kept child.
	shell := c.add(root, withClass(visible(rect(0, 0, 1080, 1000)), "android.widget.FrameLayout"))
	// Not visible ancestor with a visible descendant: becomes context.
	hidden := c.add(shell, withText(withClass(device.RawNode{}, "android.widget.LinearLayout"), "hidden wrapper"))
	c.add(hidden, withText(visible(rect(10, 20, 500, 90)), "标题"))
	// Invisible leaf without descendants: dropped.
	c.add(shell, withText(device.RawNode{}, "off-screen text"))
	// Visible but semantically empty leaf: dropped.
	c.add(root, visible(rect(5, 1000, 50, 1050)))
	// Node without bounds and without semantics is dropped as well.
	c.add(root, device.RawNode{Visible: true})

	snap := buildOne(t, NewBuilder(), c.capture())
	tree := snap.Tree

	if len(tree.Root.Children) != 1 {
		t.Fatalf("root children = %d\n%s", len(tree.Root.Children), dump(tree))
	}
	shellNode := tree.Root.Children[0]
	if shellNode.Context {
		t.Fatalf("visible shell must not be a context node")
	}
	if shellNode.Bounds == nil || *shellNode.Bounds != (protocol.Bounds{Left: 0, Top: 0, Right: 1080, Bottom: 1000}) {
		t.Fatalf("shell bounds = %v", shellNode.Bounds)
	}
	if len(shellNode.Children) != 1 {
		t.Fatalf("shell children = %d\n%s", len(shellNode.Children), dump(tree))
	}
	ctx := shellNode.Children[0]
	if !ctx.Context || ctx.Role != protocol.RoleGroup {
		t.Fatalf("hidden ancestor must be a group context node: %+v", ctx)
	}
	if ctx.Bounds != nil {
		t.Fatalf("context bounds must be nil, got %v", ctx.Bounds)
	}
	if len(ctx.Actions) != 0 || len(ctx.Label) != 0 {
		t.Fatalf("context node must have no actions or label: %+v", ctx)
	}
	if ctx.Enabled != nil || ctx.Focused != nil || ctx.Selected != nil ||
		ctx.Protected != nil || ctx.Checked != nil {
		t.Fatalf("context node must keep all five tri-state flags nil: %+v", ctx)
	}
	if _, ok := snap.Source[ctx.Ref]; ok {
		t.Fatalf("context node must not expose a source index")
	}
	if len(ctx.Children) != 1 || ctx.Children[0].Label[0] != "标题" {
		t.Fatalf("context must keep reading order: %s", dump(tree))
	}
	if _, ok := snap.Source[ctx.Children[0].Ref]; !ok {
		t.Fatalf("visible child must map to its source")
	}
	for _, ref := range snap.Refs {
		if find(tree, ref) == nil {
			t.Fatalf("ref %q is not in the tree", ref)
		}
	}
}

func TestBuildRoleAndActionMapping(t *testing.T) {
	c := newCap(1080, 2400)
	root := c.root()
	edit := c.add(root, editable(withClass(withText(visible(rect(0, 0, 100, 50)), "abc"), "android.widget.EditText")))
	check := c.add(root, checkable(withClass(withText(visible(rect(0, 60, 100, 110)), "check"), "android.widget.CheckBox"), true))
	radio := c.add(root, checkable(withClass(withText(visible(rect(0, 120, 100, 170)), "radio"), "android.widget.RadioButton"), false))
	sw := c.add(root, checkable(withClass(withText(visible(rect(0, 180, 100, 230)), "switch"), "android.widget.Switch"), true))
	toggleState := withClass(visible(rect(0, 240, 100, 290)), "com.example.ToggleChip")
	toggleState.HasCheckable, toggleState.Checkable = true, true
	toggle := c.add(root, toggleState)
	scroll := c.add(root, scrollable(visible(rect(0, 300, 1080, 1000))))
	button := c.add(root, clickable(longClickable(withClass(withText(visible(rect(0, 1010, 100, 1060)), "确定"), "android.widget.Button"))))
	plainClick := c.add(root, clickable(visible(rect(0, 1070, 100, 1120))))
	imageNode := withClass(visible(rect(0, 1130, 100, 1180)), "android.widget.ImageView")
	imageNode.HasEnabled = true
	image := c.add(root, imageNode)
	textNode := c.add(root, withText(withClass(visible(rect(0, 1190, 100, 1240)), "android.widget.TextView"), "正文"))
	view := c.add(root, withText(withClass(visible(rect(0, 1250, 100, 1300)), "com.example.CustomView"), "自定义"))
	toolbar := c.add(root, clickable(withDesc(withClass(visible(rect(0, 1310, 100, 1360)), "android.widget.LinearLayout"), "返回")))

	snap := buildOne(t, NewBuilder(), c.capture())
	tree := snap.Tree
	want := []struct {
		index  int
		role   protocol.Role
		action []protocol.Action
	}{
		{edit, protocol.RoleInput, []protocol.Action{protocol.ActionText}},
		{check, protocol.RoleCheckbox, nil},
		{radio, protocol.RoleRadio, nil},
		{sw, protocol.RoleSwitch, nil},
		{toggle, protocol.RoleToggle, nil},
		{scroll, protocol.RoleScroll, []protocol.Action{protocol.ActionSwipe}},
		{button, protocol.RoleButton, []protocol.Action{protocol.ActionTap, protocol.ActionLongPress}},
		{plainClick, protocol.RoleView, []protocol.Action{protocol.ActionTap}},
		{image, protocol.RoleImage, nil},
		{textNode, protocol.RoleText, nil},
		{view, protocol.RoleView, nil},
		{toolbar, protocol.RoleButton, []protocol.Action{protocol.ActionTap}},
	}
	if len(tree.Root.Children) != len(want) {
		t.Fatalf("children = %d, want %d\n%s", len(tree.Root.Children), len(want), dump(tree))
	}
	for i, w := range want {
		node := tree.Root.Children[i]
		if node.Role != w.role {
			t.Fatalf("node %d (%s) role = %s, want %s", i, tree.Root.Children[i].Label, node.Role, w.role)
		}
		if len(node.Actions) != len(w.action) {
			t.Fatalf("node %d actions = %v, want %v", i, node.Actions, w.action)
		}
		for j := range w.action {
			if node.Actions[j] != w.action[j] {
				t.Fatalf("node %d action order = %v, want %v", i, node.Actions, w.action)
			}
		}
	}
}

func TestBuildCheckedValueAndDefaults(t *testing.T) {
	c := newCap(1080, 2400)
	root := c.root()
	// Input with known empty value.
	c.add(root, editable(withClass(withDesc(visible(rect(0, 0, 100, 50)), "搜索"), "android.widget.EditText")))
	// Input whose text was not obtained: value null.
	unknown := enabled(editable(withClass(visible(rect(0, 60, 100, 110)), "android.widget.EditText")), false)
	c.add(root, unknown)
	// Password input: protected, value null, text not published.
	c.add(root, password(editable(withText(withClass(visible(rect(0, 120, 100, 170)), "secret"), "android.widget.EditText"))))
	// Checkable without source checked attribute: explicit null.
	noState := withClass(visible(rect(0, 180, 100, 230)), "android.widget.CheckBox")
	noState.HasCheckable, noState.Checkable = true, true
	c.add(root, noState)
	// Non-checkable role with a reported checked state keeps it explicit.
	c.add(root, withText(selected(focused(withClass(visible(rect(0, 240, 100, 290)), "android.widget.TextView"), true), true), "选中"))
	// Unknown enabled: null.
	unknownEnabled := withText(visible(rect(0, 300, 100, 350)), "未知启用")

	c.nodes[1].Text, c.nodes[1].TextSet = "", true
	c.nodes[1].HasEnabled = false
	c.add(root, unknownEnabled)

	snap := buildOne(t, NewBuilder(), c.capture())
	children := snap.Tree.Root.Children
	if len(children) != 6 {
		t.Fatalf("children = %d\n%s", len(children), dump(snap.Tree))
	}
	if children[0].Enabled != nil {
		t.Fatalf("input without source enabled must publish null")
	}
	search := children[0]
	if search.Role != protocol.RoleInput {
		t.Fatalf("search role = %s", search.Role)
	}
	if search.Value == nil || *search.Value != "" {
		t.Fatalf("search value = %v, want known empty", search.Value)
	}
	if len(search.Label) != 1 || search.Label[0] != "搜索" {
		t.Fatalf("search label = %v; an input keeps its description as label", search.Label)
	}

	unknownValue := children[1]
	if unknownValue.Value != nil {
		t.Fatalf("input without obtained text must publish null value")
	}
	if unknownValue.Enabled == nil || *unknownValue.Enabled {
		t.Fatalf("known disabled input must publish enabled=false")
	}

	secret := children[2]
	if secret.Protected == nil || !*secret.Protected {
		t.Fatalf("password must publish protected=true")
	}
	if secret.Value != nil || len(secret.Label) != 0 {
		t.Fatalf("password must not publish text: %s", dump(snap.Tree))
	}

	unchecked := children[3]
	if unchecked.Checked != nil {
		t.Fatalf("checkable without source state must publish explicit null, got %t", *unchecked.Checked)
	}

	text := children[4]
	if text.Selected == nil || !*text.Selected || text.Focused == nil || !*text.Focused {
		t.Fatalf("reported focused/selected must be kept: %+v", text)
	}
	if text.Checked != nil {
		t.Fatalf("non-checkable role without checked must keep it null")
	}

	last := children[5]
	if last.Enabled != nil {
		t.Fatalf("unknown enabled must be null, got %t", *last.Enabled)
	}
}

func TestBuildControlPackageNodesStay(t *testing.T) {
	// The projection never filters by package: a control-app node is a normal
	// public unit. Filtering, if any, belongs to the engine's scope decision.
	c := newCap(1080, 2400)
	root := c.root()
	c.add(root, withText(withPkg(withClass(visible(rect(0, 0, 100, 50)), "android.widget.TextView"), "com.example.control"), "控制台"))
	snap := buildOne(t, NewBuilder(), c.capture())
	if len(snap.Tree.Root.Children) != 1 || snap.Tree.Root.Children[0].Label[0] != "控制台" {
		t.Fatalf("control package node must stay: %s", dump(snap.Tree))
	}
}

func TestBuildBoundsClippingAndResourceHint(t *testing.T) {
	c := newCap(100, 100)
	root := c.root()
	// Partly off-screen: clipped to the display rectangle.
	partial := visible(rect(-20, 80, 60, 140))
	c.add(root, withText(partial, "部分"))
	// Unlabelled clickable unit gets its resource id as a source hint.
	c.add(root, withRes(clickable(visible(rect(10, 10, 20, 20))), "com.example:id/side_entry"))
	// Labelled unit does not publish the resource hint.
	c.add(root, withRes(clickable(withText(visible(rect(30, 30, 40, 40)), "确定")), "com.example:id/ok"))

	snap := buildOne(t, NewBuilder(), c.capture())
	children := snap.Tree.Root.Children
	if got := children[0].Bounds; got == nil || *got != (protocol.Bounds{Left: 0, Top: 80, Right: 60, Bottom: 100}) {
		t.Fatalf("clipped bounds = %v", got)
	}
	if children[1].Resource == nil || *children[1].Resource != "com.example:id/side_entry" {
		t.Fatalf("unlabelled clickable resource hint = %v", children[1].Resource)
	}
	if children[2].Resource != nil {
		t.Fatalf("labelled unit must not publish resource, got %v", *children[2].Resource)
	}
}

func TestBuildRefsArePreorderAndMonotonic(t *testing.T) {
	c := newCap(1080, 2400)
	root := c.root()
	a := c.add(root, withText(visible(rect(0, 0, 10, 10)), "a"))
	c.add(a, withText(visible(rect(0, 0, 10, 10)), "a1"))
	c.add(root, withText(visible(rect(0, 0, 10, 10)), "b"))

	snap := buildOne(t, NewBuilder(), c.capture())
	want := []string{"n1", "n2", "n3", "n4"}
	if len(snap.Refs) != len(want) {
		t.Fatalf("refs = %v", snap.Refs)
	}
	for i := range want {
		if snap.Refs[i] != want[i] {
			t.Fatalf("refs = %v, want preorder %v", snap.Refs, want)
		}
	}
}

func TestBuildDoesNotMutatePreviousSnapshot(t *testing.T) {
	c := newCap(1080, 2400)
	root := c.root()
	c.add(root, withText(visible(rect(0, 0, 100, 50)), "状态"))
	b := NewBuilder()
	first := buildOne(t, b, c.capture())
	before := dump(first.Tree)

	c2 := newCap(1080, 2400)
	root2 := c2.root()
	c2.add(root2, withText(visible(rect(0, 0, 100, 50)), "状态"))
	c2.add(root2, withText(visible(rect(0, 60, 100, 110)), "新增"))
	second := buildOne(t, b, c2.capture())
	if dump(first.Tree) != before {
		t.Fatalf("previous snapshot was mutated:\nbefore:\n%s\nafter:\n%s", before, dump(first.Tree))
	}
	if len(second.Tree.Root.Children) != 2 {
		t.Fatalf("second tree children = %d", len(second.Tree.Root.Children))
	}
	if b.Previous() != second {
		t.Fatalf("Previous must return the last snapshot")
	}
}

func TestBuilderReset(t *testing.T) {
	c := newCap(1080, 2400)
	root := c.root()
	c.add(root, withText(visible(rect(0, 0, 100, 50)), "a"))
	b := NewBuilder()
	buildOne(t, b, c.capture())
	b.Reset()
	if b.Previous() != nil {
		t.Fatalf("Reset must discard the previous snapshot")
	}
	snap := buildOne(t, b, c.capture())
	if snap.Tree.Root.Ref != "n1" || snap.Tree.Root.Children[0].Ref != "n2" {
		t.Fatalf("refs must restart after Reset: %s", strings.Join(snap.Refs, ","))
	}
}
