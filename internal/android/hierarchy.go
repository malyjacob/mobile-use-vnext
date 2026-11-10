package android

import (
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// maxHierarchyDepth is the dumpWindowHierarchy max-depth argument. It matches
// the pinned client default so neither side silently truncates the tree.
const maxHierarchyDepth = 50

// maxHierarchyNodes bounds one parse so a malformed dump cannot exhaust memory.
const maxHierarchyNodes = 20000

// xmlElement is one decoded hierarchy element. Attribute presence is kept
// because a missing attribute must not be turned into a known default.
type xmlElement struct {
	attrs    map[string]string
	children []*xmlElement
}

// parseHierarchyXML decodes the uiautomator dump with encoding/xml and keeps
// every element with its attribute map. Malformed XML is a controlled error,
// never an empty result.
func parseHierarchyXML(data []byte) (*xmlElement, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	var (
		stack []*xmlElement
		root  *xmlElement
		count int
	)
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, newErr(CodeHierarchy, "the hierarchy XML could not be parsed", nil)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			element := &xmlElement{attrs: make(map[string]string, len(typed.Attr))}
			for _, attr := range typed.Attr {
				element.attrs[attr.Name.Local] = attr.Value
			}
			if len(stack) == 0 {
				root = element
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, element)
			}
			stack = append(stack, element)
			count++
			if count > maxHierarchyNodes {
				return nil, newErr(CodeHierarchy, "the hierarchy is too large", nil)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, newErr(CodeHierarchy, "the hierarchy XML is not well formed", nil)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 || root == nil {
		return nil, newErr(CodeHierarchy, "the hierarchy XML is empty or truncated", nil)
	}
	return root, nil
}

// parseHierarchy filters one dump to a single logical display.
//
// A node without display-id inherits its nearest ancestor's value; a node with
// its own attribute overrides it. Only nodes whose effective display id equals
// targetDisplay are returned, preserving source order and parent links for all
// of them, including off-screen ones. A missing display id never maps to
// display zero. The second result is the traversal root of the kept tree; when
// the top-level display root itself is not on the target display, that root is
// the first kept node in source order.
func parseHierarchy(data []byte, targetDisplay, width, height int) ([]device.RawNode, int, error) {
	if targetDisplay < 0 {
		return nil, -1, newErr(CodeInvalidArgs, "invalid display id", nil)
	}
	if width <= 0 || height <= 0 {
		return nil, -1, newErr(CodeInvalidArgs, "invalid display size", nil)
	}
	root, err := parseHierarchyXML(data)
	if err != nil {
		return nil, -1, err
	}
	builder := &hierarchyBuilder{
		target: strconv.Itoa(targetDisplay),
		bounds: device.Rect{Left: 0, Top: 0, Right: width, Bottom: height},
	}
	var top []*xmlElement
	if rootElementName(data) == "hierarchy" {
		top = root.children
	} else {
		top = []*xmlElement{root}
	}
	inherited := hierarchyBinding{display: "", parent: -1}
	for _, element := range top {
		if err := builder.visit(element, inherited, 0); err != nil {
			return nil, -1, err
		}
	}
	if len(builder.nodes) == 0 {
		// A successfully parsed dump may legitimately contain no window on the
		// target display (a fresh virtual display has no app task). The caller
		// represents that as a synthetic empty root, not as a failed tree.
		return nil, -1, nil
	}
	linkChildren(builder.nodes)
	rootIndex := builder.keptRoot
	if rootIndex < 0 {
		rootIndex = 0
	}
	return builder.nodes, rootIndex, nil
}

// rootElementName returns the first start element's local name ("" on error).
// uiautomator wraps the dump in a <hierarchy> element; a bare node is accepted
// so single-node fixtures behave like the device output.
func rootElementName(data []byte) string {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		if start, ok := token.(xml.StartElement); ok {
			return start.Name.Local
		}
	}
}

// hierarchyBinding is the inherited display binding for a subtree.
type hierarchyBinding struct {
	display string
	parent  int
}

type hierarchyBuilder struct {
	target   string
	bounds   device.Rect
	nodes    []device.RawNode
	keptRoot int
	hasKept  bool
}

// visit evaluates one element and then its children with the new binding.
func (b *hierarchyBuilder) visit(element *xmlElement, inherited hierarchyBinding, depth int) error {
	if depth > maxHierarchyDepth {
		return newErr(CodeHierarchy, "the hierarchy is deeper than the supported limit", nil)
	}
	display := inherited.display
	parent := inherited.parent
	if own, ok := element.attrs["display-id"]; ok {
		display = own
		if own != inherited.display {
			// An explicit override breaks the inherited chain. The nearest kept
			// ancestor only remains the parent when it is still on this display.
			if inherited.display != b.target {
				parent = -1
			}
		}
	}
	if display == b.target {
		node, err := b.convert(element, parent)
		if err != nil {
			return err
		}
		index := len(b.nodes)
		node.Index = index
		node.Depth = depth
		if parent >= 0 {
			parentNode := &b.nodes[parent]
			parentNode.Children = append(parentNode.Children, index)
		}
		b.nodes = append(b.nodes, node)
		if !b.hasKept || (parent < 0 && depth < b.nodes[b.keptRoot].Depth) {
			b.keptRoot = index
			b.hasKept = true
		}
		parent = index
	}
	next := hierarchyBinding{display: display, parent: parent}
	for _, child := range element.children {
		if err := b.visit(child, next, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// convert builds one raw node from its element attributes.
func (b *hierarchyBuilder) convert(element *xmlElement, parent int) (device.RawNode, error) {
	node := device.RawNode{Parent: parent, DisplayID: displayIDOf(b.target)}
	node.Text, node.TextSet = element.attrs["text"]
	node.Description, node.DescSet = element.attrs["content-desc"]
	node.ResourceID, node.ResourceSet = element.attrs["resource-id"]
	node.ClassName = element.attrs["class"]
	node.Package = element.attrs["package"]

	applyFlag(&node.HasEnabled, &node.Enabled, element.attrs, "enabled")
	applyFlag(&node.HasChecked, &node.Checked, element.attrs, "checked")
	applyFlag(&node.HasSelected, &node.Selected, element.attrs, "selected")
	applyFlag(&node.HasFocused, &node.Focused, element.attrs, "focused")
	applyFlag(&node.HasClickable, &node.Clickable, element.attrs, "clickable")
	applyFlag(&node.HasLongClick, &node.LongClickable, element.attrs, "long-clickable")
	applyFlag(&node.HasScrollable, &node.Scrollable, element.attrs, "scrollable")
	applyFlag(&node.HasCheckable, &node.Checkable, element.attrs, "checkable")
	applyFlag(&node.HasPassword, &node.Password, element.attrs, "password")
	node.Editable = strings.HasSuffix(node.ClassName, "EditText")

	raw, ok := parseBounds(element.attrs["bounds"])
	if !ok {
		// Invalid or absent bounds are off-screen evidence; nothing is fabricated.
		return node, nil
	}
	node.RawBounds = raw
	clipped := raw.Intersect(b.bounds)
	if !clipped.Empty() {
		node.Bounds = clipped
	}
	node.Visible = element.attrs["visible-to-user"] != "false" && !clipped.Empty()
	return node, nil
}

func displayIDOf(value string) int {
	id, _ := strconv.Atoi(value)
	return id
}

// linkChildren re-derives child lists deterministically from parent links.
func linkChildren(nodes []device.RawNode) {
	for index := range nodes {
		nodes[index].Children = nil
	}
	for index := range nodes {
		parent := nodes[index].Parent
		if parent >= 0 && parent < len(nodes) {
			nodes[parent].Children = append(nodes[parent].Children, index)
		}
	}
}

// displayRootNode builds the single traversal root of a display capture. The
// public projection always replaces it with its own synthetic root, so this
// node carries only display identity, bounds and the known enabled state.
func displayRootNode(displayID, width, height int) device.RawNode {
	bounds := device.Rect{Left: 0, Top: 0, Right: width, Bottom: height}
	return device.RawNode{
		Parent: -1, DisplayID: displayID, Visible: true,
		RawBounds: bounds, Bounds: bounds,
		HasEnabled: true, Enabled: true,
	}
}

// connectDisplayWindows attaches every top-level window of one display to a
// single synthetic root. A u2 dump on the primary display may contain several
// top-level windows (status bar, app, IME, overlays); they are one display, not
// separate trees. A dump with one top-level window keeps that window as root.
func connectDisplayWindows(nodes []device.RawNode, root, displayID, width, height int) ([]device.RawNode, int) {
	topLevel := 0
	for index := range nodes {
		if nodes[index].Parent < 0 {
			topLevel++
		}
	}
	if topLevel <= 1 {
		return nodes, root
	}
	synthetic := displayRootNode(displayID, width, height)
	synthetic.Index = len(nodes)
	nodes = append(nodes, synthetic)
	for index := range nodes {
		if index != synthetic.Index && nodes[index].Parent < 0 {
			nodes[index].Parent = synthetic.Index
		}
	}
	linkChildren(nodes)
	return nodes, synthetic.Index
}

// applyFlag writes one presence flag and its value into the raw node.
func applyFlag(has, value *bool, attrs map[string]string, name string) {
	attr := boolAttr(attrs, name)
	*has = attr.present
	*value = attr.value
}

// flagAttr is one tri-state source attribute: present tells whether the dump
// reported it, value is what it said.
type flagAttr struct {
	present bool
	value   bool
}

// boolAttr reports presence and the parsed boolean; an unparseable value is
// reported as false but remains present.
func boolAttr(attrs map[string]string, name string) flagAttr {
	value, ok := attrs[name]
	return flagAttr{present: ok, value: value == "true"}
}

// parseBounds reads Android's `[left,top][right,bottom]` form strictly.
func parseBounds(raw string) (device.Rect, bool) {
	match := boundsPattern.FindStringSubmatch(raw)
	if match == nil {
		return device.Rect{}, false
	}
	values := [4]int{}
	for index, group := range match[1:] {
		parsed, err := strconv.Atoi(group)
		if err != nil {
			return device.Rect{}, false
		}
		values[index] = parsed
	}
	return device.Rect{Left: values[0], Top: values[1], Right: values[2], Bottom: values[3]}, true
}
