package observation

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// nodeByLabel returns the first public node whose leading label is label. The
// synthetic labels keep these tests independent of ref numbering.
func nodeByLabel(t *testing.T, tree *protocol.Tree, label string) *protocol.Node {
	t.Helper()
	var found *protocol.Node
	var walk func(n *protocol.Node)
	walk = func(n *protocol.Node) {
		if n == nil || found != nil {
			return
		}
		if len(n.Label) > 0 && n.Label[0] == label {
			found = n
			return
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(tree.Root)
	if found == nil {
		t.Fatalf("node %q not found in\n%s", label, dump(tree))
	}
	return found
}

// requireScrollAxis asserts that the labelled node is a public scroll container
// with the projected axis. Requiring the scroll role keeps a "no axis" result
// meaningful: it proves the projection declined rather than the node never
// taking part in it.
func requireScrollAxis(t *testing.T, tree *protocol.Tree, label string, want protocol.Axis) {
	t.Helper()
	node := nodeByLabel(t, tree, label)
	if node.Role != protocol.RoleScroll {
		t.Fatalf("%q role = %s, want scroll\n%s", label, node.Role, dump(tree))
	}
	if node.Bounds == nil {
		t.Fatalf("%q has no public geometry\n%s", label, dump(tree))
	}
	if node.Axis != want {
		t.Fatalf("%q axis = %q, want %q\n%s", label, node.Axis, want, dump(tree))
	}
}

// TestScrollAxisFromClassName covers the first projection level: a fixed scroll
// direction carried by a native container class name.
func TestScrollAxisFromClassName(t *testing.T) {
	cases := []struct {
		name  string
		class string
		want  protocol.Axis
	}{
		{"horizontal scroll view", "android.widget.HorizontalScrollView", protocol.AxisX},
		{"scroll view", "android.widget.ScrollView", protocol.AxisY},
		{"nested scroll view", "androidx.core.widget.NestedScrollView", protocol.AxisY},
		{"list view", "android.widget.ListView", protocol.AxisY},
		{"abs list view", "android.widget.AbsListView", protocol.AxisY},
		{"view pager", "androidx.viewpager.widget.ViewPager", protocol.AxisX},
		{"view pager 2", "androidx.viewpager2.widget.ViewPager2", protocol.AxisX},
		{"vertical view pager", "androidx.viewpager2.widget.VerticalViewPager2", protocol.AxisY},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCap(1080, 2400)
			root := c.root()
			// One non-scroll child gives the child-layout level nothing to
			// sample, so the class name alone must decide.
			container := c.add(root, withText(withClass(scrollable(visible(rect(0, 100, 1080, 900))), tc.class), "容器"))
			c.add(container, withText(visible(rect(0, 100, 1080, 200)), "条目"))

			snap := buildOne(t, NewBuilder(), c.capture())
			requireScrollAxis(t, snap.Tree, "容器", tc.want)
		})
	}
}

// TestScrollAxisFromChildLayout covers the second projection level: the layout
// of the published children, which is the only signal a generic RecyclerView or
// custom ViewGroup provides.
func TestScrollAxisFromChildLayout(t *testing.T) {
	t.Run("horizontal recycler", func(t *testing.T) {
		c := newCap(1080, 2400)
		root := c.root()
		container := c.add(root, withText(withClass(scrollable(visible(rect(0, 200, 1080, 600))), "androidx.recyclerview.widget.RecyclerView"), "容器"))
		c.add(container, withText(visible(rect(0, 200, 300, 500)), "卡片一"))
		c.add(container, withText(visible(rect(300, 200, 600, 500)), "卡片二"))
		c.add(container, withText(visible(rect(600, 200, 900, 500)), "卡片三"))

		snap := buildOne(t, NewBuilder(), c.capture())
		requireScrollAxis(t, snap.Tree, "容器", protocol.AxisX)
	})

	t.Run("vertical recycler", func(t *testing.T) {
		c := newCap(1080, 2400)
		root := c.root()
		container := c.add(root, withText(withClass(scrollable(visible(rect(0, 200, 1080, 1600))), "androidx.recyclerview.widget.RecyclerView"), "容器"))
		c.add(container, withText(visible(rect(0, 200, 1080, 600)), "条目一"))
		c.add(container, withText(visible(rect(0, 600, 1080, 1000)), "条目二"))
		c.add(container, withText(visible(rect(0, 1000, 1080, 1600)), "条目三"))

		snap := buildOne(t, NewBuilder(), c.capture())
		requireScrollAxis(t, snap.Tree, "容器", protocol.AxisY)
	})

	t.Run("single content wrapper", func(t *testing.T) {
		// The common "scroll container > one root content group > items"
		// structure: the wrapper is not a scroll container, so the sampler
		// expands through it one level.
		c := newCap(1080, 2400)
		root := c.root()
		container := c.add(root, withText(withClass(scrollable(visible(rect(0, 1600, 1080, 1900))), "androidx.recyclerview.widget.RecyclerView"), "容器"))
		wrapper := c.add(container, withClass(visible(rect(0, 1600, 1080, 1900)), "android.widget.FrameLayout"))
		c.add(wrapper, withText(visible(rect(0, 1600, 540, 1900)), "左"))
		c.add(wrapper, withText(visible(rect(540, 1600, 1080, 1900)), "右"))

		snap := buildOne(t, NewBuilder(), c.capture())
		requireScrollAxis(t, snap.Tree, "容器", protocol.AxisX)
	})

	t.Run("child layout overrides class name", func(t *testing.T) {
		// A horizontal class name whose children actually stack vertically must
		// follow the observed layout.
		c := newCap(1080, 2400)
		root := c.root()
		container := c.add(root, withText(withClass(scrollable(visible(rect(0, 0, 1080, 600))), "android.widget.HorizontalScrollView"), "容器"))
		c.add(container, withText(visible(rect(0, 0, 1080, 180)), "段一"))
		c.add(container, withText(visible(rect(0, 200, 1080, 380)), "段二"))
		c.add(container, withText(visible(rect(0, 400, 1080, 580)), "段三"))

		snap := buildOne(t, NewBuilder(), c.capture())
		requireScrollAxis(t, snap.Tree, "容器", protocol.AxisY)
	})

	t.Run("generic class with horizontal children", func(t *testing.T) {
		// Reverse control for the child-layout level: the class name alone
		// proves nothing, so a "x" result can only come from the sampler that
		// populateScrollAxes runs during the build.
		c := newCap(1080, 2400)
		root := c.root()
		container := c.add(root, withText(withClass(scrollable(visible(rect(0, 100, 1080, 500))), "com.example.widget.Carousel"), "容器"))
		c.add(container, withText(visible(rect(0, 100, 500, 500)), "轮播一"))
		c.add(container, withText(visible(rect(500, 100, 1000, 500)), "轮播二"))

		snap := buildOne(t, NewBuilder(), c.capture())
		requireScrollAxis(t, snap.Tree, "容器", protocol.AxisX)
	})

	t.Run("two dimensional grid stays unknown", func(t *testing.T) {
		c := newCap(1080, 2400)
		root := c.root()
		container := c.add(root, withText(withClass(scrollable(visible(rect(0, 0, 800, 800))), "androidx.recyclerview.widget.RecyclerView"), "容器"))
		c.add(container, withText(visible(rect(0, 0, 400, 400)), "格一"))
		c.add(container, withText(visible(rect(400, 0, 800, 400)), "格二"))
		c.add(container, withText(visible(rect(0, 400, 400, 800)), "格三"))
		c.add(container, withText(visible(rect(400, 400, 800, 800)), "格四"))

		snap := buildOne(t, NewBuilder(), c.capture())
		requireScrollAxis(t, snap.Tree, "容器", "")
	})

	t.Run("single child stays unknown", func(t *testing.T) {
		c := newCap(1080, 2400)
		root := c.root()
		container := c.add(root, withText(withClass(scrollable(visible(rect(0, 100, 1080, 900))), "com.example.widget.Panel"), "容器"))
		c.add(container, withText(visible(rect(0, 100, 1080, 900)), "唯一子项"))

		snap := buildOne(t, NewBuilder(), c.capture())
		requireScrollAxis(t, snap.Tree, "容器", "")
	})

	t.Run("no children stays unknown", func(t *testing.T) {
		// Negative control: without class or child evidence the projection must
		// leave the field empty instead of inventing an axis.
		c := newCap(1080, 2400)
		root := c.root()
		c.add(root, withText(withClass(scrollable(visible(rect(0, 100, 1080, 900))), "com.example.widget.Panel"), "容器"))

		snap := buildOne(t, NewBuilder(), c.capture())
		requireScrollAxis(t, snap.Tree, "容器", "")
	})
}

// TestScrollAxisSamplesAreBounded proves the child sampler only looks at the
// leading children: the 17th item sits far below the first 16, which would turn
// the layout into a two-dimensional one if it were sampled.
func TestScrollAxisSamplesAreBounded(t *testing.T) {
	c := newCap(1080, 2400)
	root := c.root()
	container := c.add(root, withText(withClass(scrollable(visible(rect(0, 0, 1080, 400))), "androidx.recyclerview.widget.RecyclerView"), "容器"))
	for i := 0; i < scrollAxisMaxSamples; i++ {
		c.add(container, withText(visible(rect(i*65, 0, i*65+60, 100)), "条目"))
	}
	c.add(container, withText(visible(rect(0, 2000, 100, 2100)), "远端条目"))

	snap := buildOne(t, NewBuilder(), c.capture())
	requireScrollAxis(t, snap.Tree, "容器", protocol.AxisX)
}

// TestScrollAxisIgnoresNonScrollRoles keeps the projection on scroll
// containers: a button whose children happen to be spread along one axis owns
// no scroll capability and must not publish an axis.
func TestScrollAxisIgnoresNonScrollRoles(t *testing.T) {
	c := newCap(1080, 2400)
	root := c.root()
	button := c.add(root, clickable(withText(withClass(visible(rect(0, 100, 1080, 500)), "android.widget.Button"), "操作")))
	c.add(button, withText(visible(rect(0, 100, 500, 500)), "左"))
	c.add(button, withText(visible(rect(500, 100, 1000, 500)), "右"))

	snap := buildOne(t, NewBuilder(), c.capture())
	node := nodeByLabel(t, snap.Tree, "操作")
	if node.Role != protocol.RoleButton {
		t.Fatalf("role = %s, want button\n%s", node.Role, dump(snap.Tree))
	}
	if node.Axis != "" {
		t.Fatalf("non-scroll node axis = %q, want empty\n%s", node.Axis, dump(snap.Tree))
	}
}

// TestScrollAxisContextNodeStaysUnknown keeps context nodes out: they carry no
// public geometry and must never publish an axis either.
func TestScrollAxisContextNodeStaysUnknown(t *testing.T) {
	c := newCap(1080, 2400)
	root := c.root()
	// An invisible scroll ancestor of visible content becomes a context group.
	hidden := c.add(root, withClass(scrollable(device.RawNode{}), "android.widget.HorizontalScrollView"))
	c.add(hidden, withText(visible(rect(0, 100, 540, 300)), "左"))
	c.add(hidden, withText(visible(rect(540, 100, 1080, 300)), "右"))

	snap := buildOne(t, NewBuilder(), c.capture())
	ctx := findContextNode(snap.Tree.Root)
	if ctx == nil {
		t.Fatalf("expected a context ancestor\n%s", dump(snap.Tree))
	}
	if ctx.Role != protocol.RoleGroup {
		t.Fatalf("context role = %s, want group", ctx.Role)
	}
	if ctx.Axis != "" {
		t.Fatalf("context node axis = %q, want empty", ctx.Axis)
	}
}

func findContextNode(n *protocol.Node) *protocol.Node {
	if n == nil {
		return nil
	}
	if n.Context {
		return n
	}
	for _, child := range n.Children {
		if found := findContextNode(child); found != nil {
			return found
		}
	}
	return nil
}

// TestInferScrollAxisFromClassKeepsContainmentOrder checks the ordered class
// rules directly, including the HorizontalScrollView-before-ScrollView case.
func TestInferScrollAxisFromClassKeepsContainmentOrder(t *testing.T) {
	cases := map[string]protocol.Axis{
		"android.widget.HorizontalScrollView":       protocol.AxisX,
		"android.widget.ScrollView":                 protocol.AxisY,
		"android.widget.ListView":                   protocol.AxisY,
		"android.widget.AbsListView":                protocol.AxisY,
		"androidx.viewpager.widget.ViewPager":       protocol.AxisX,
		"com.example.VerticalViewPager":             protocol.AxisY,
		"com.example.HorizontalOuterScrollView":     "",
		"android.widget.FrameLayout":                "",
		"androidx.recyclerview.widget.RecyclerView": "",
	}
	for class, want := range cases {
		if got := inferScrollAxisFromClass(class); got != want {
			t.Fatalf("%s axis = %q, want %q", class, got, want)
		}
	}
}
