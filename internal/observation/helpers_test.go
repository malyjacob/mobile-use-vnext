package observation

import (
	"fmt"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// capBuilder assembles a controlled device.Capture for tests. All data is
// synthetic; no device or network is involved.
type capBuilder struct {
	display device.Display
	nodes   []device.RawNode
}

func newCap(width, height int) *capBuilder {
	return &capBuilder{display: device.Display{ID: 0, Width: width, Height: height, Rotation: 0}}
}

// add appends a raw node and returns its index. Parent -1 makes it the root.
func (c *capBuilder) add(parent int, raw device.RawNode) int {
	index := len(c.nodes)
	raw.Index = index
	raw.Parent = parent
	raw.DisplayID = c.display.ID
	if raw.Depth == 0 && parent >= 0 {
		raw.Depth = c.nodes[parent].Depth + 1
	}
	c.nodes = append(c.nodes, raw)
	if parent >= 0 {
		c.nodes[parent].Children = append(c.nodes[parent].Children, index)
	}
	return index
}

// root appends the traversal root.
func (c *capBuilder) root() int { return c.add(-1, device.RawNode{}) }

func (c *capBuilder) capture() *device.Capture {
	return &device.Capture{Display: c.display, Nodes: c.nodes, Root: 0}
}

// raw helpers

func visible(b device.Rect) device.RawNode {
	return device.RawNode{Visible: true, RawBounds: b, Bounds: b}
}

func withText(raw device.RawNode, text string) device.RawNode {
	raw.Text, raw.TextSet = text, true
	return raw
}

func withDesc(raw device.RawNode, desc string) device.RawNode {
	raw.Description, raw.DescSet = desc, true
	return raw
}

func withRes(raw device.RawNode, res string) device.RawNode {
	raw.ResourceID, raw.ResourceSet = res, true
	return raw
}

func withClass(raw device.RawNode, class string) device.RawNode {
	raw.ClassName = class
	return raw
}

func withPkg(raw device.RawNode, pkg string) device.RawNode {
	raw.Package = pkg
	return raw
}

func clickable(raw device.RawNode) device.RawNode {
	raw.HasClickable, raw.Clickable = true, true
	return raw
}

func longClickable(raw device.RawNode) device.RawNode {
	raw.HasLongClick, raw.LongClickable = true, true
	return raw
}

func scrollable(raw device.RawNode) device.RawNode {
	raw.HasScrollable, raw.Scrollable = true, true
	return raw
}

func editable(raw device.RawNode) device.RawNode {
	raw.Editable = true
	return raw
}

func checkable(raw device.RawNode, checked bool) device.RawNode {
	raw.HasCheckable, raw.Checkable = true, true
	raw.HasChecked, raw.Checked = true, checked
	return raw
}

func enabled(raw device.RawNode, value bool) device.RawNode {
	raw.HasEnabled, raw.Enabled = true, value
	return raw
}

func selected(raw device.RawNode, value bool) device.RawNode {
	raw.HasSelected, raw.Selected = true, value
	return raw
}

func focused(raw device.RawNode, value bool) device.RawNode {
	raw.HasFocused, raw.Focused = true, value
	return raw
}

func password(raw device.RawNode) device.RawNode {
	raw.HasPassword, raw.Password = true, true
	return raw
}

func rect(l, t, r, b int) device.Rect { return device.Rect{Left: l, Top: t, Right: r, Bottom: b} }

// public tree helpers

// find returns the node with ref inside tree, or nil.
func find(tree *protocol.Tree, ref string) *protocol.Node {
	var walk func(n *protocol.Node) *protocol.Node
	walk = func(n *protocol.Node) *protocol.Node {
		if n == nil {
			return nil
		}
		if n.Ref == ref {
			return n
		}
		for _, child := range n.Children {
			if found := walk(child); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(tree.Root)
}

// dump renders a compact preorder description for failure messages.
func dump(tree *protocol.Tree) string {
	var b strings.Builder
	var walk func(n *protocol.Node, depth int)
	walk = func(n *protocol.Node, depth int) {
		fmt.Fprintf(&b, "%s%s %s label=%v actions=%v bounds=%v ctx=%t",
			strings.Repeat("  ", depth), n.Ref, n.Role, n.Label, n.Actions, n.Bounds, n.Context)
		if n.Checked != nil {
			fmt.Fprintf(&b, " checked=%t", *n.Checked)
		}
		if n.Value != nil {
			fmt.Fprintf(&b, " value=%q", *n.Value)
		}
		if n.Enabled == nil {
			b.WriteString(" enabled=null")
		}
		b.WriteString("\n")
		for _, child := range n.Children {
			walk(child, depth+1)
		}
	}
	walk(tree.Root, 0)
	return b.String()
}

func buildOne(t *testing.T, builder *Builder, cap *device.Capture) *Snapshot {
	t.Helper()
	snap, err := builder.Build(cap)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return snap
}
