package observation

import (
	"fmt"
	"strconv"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Equal compares two trees after canonical default normalization.
//
// Two nodes are equal when every logical field is equal. The encoding default
// is treated as the value it promises:
//   - a missing (zero value) enabled means known true, so nil Enabled equals
//     a pointer to true;
//   - focused/selected/protected nil equals false;
//   - an empty label/actions slice equals nil;
//   - context false equals the zero value;
//   - role, bounds, value, hint (tri-state strings), checked and axis are
//     compared exactly, because their zero value means "not known" and is not
//     interchangeable with a known value.
//
// Two trees whose encodings differ still compare equal when they encode the
// same logical content.
func Equal(a, b *protocol.Tree) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Format != b.Format || a.Scope != b.Scope {
		return false
	}
	return equalNode(a.Root, b.Root)
}

func equalNode(a, b *protocol.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Ref != b.Ref || a.Role != b.Role || a.Context != b.Context {
		return false
	}
	if (a.Bounds == nil) != (b.Bounds == nil) {
		return false
	}
	if a.Bounds != nil && *a.Bounds != *b.Bounds {
		return false
	}
	if len(a.Label) != len(b.Label) {
		return false
	}
	for i := range a.Label {
		if a.Label[i] != b.Label[i] {
			return false
		}
	}
	if len(a.Actions) != len(b.Actions) {
		return false
	}
	for i := range a.Actions {
		if a.Actions[i] != b.Actions[i] {
			return false
		}
	}
	if !equalTriBool(a.Enabled, b.Enabled, true) {
		return false
	}
	if !equalTriBool(a.Focused, b.Focused, false) {
		return false
	}
	if !equalTriBool(a.Selected, b.Selected, false) {
		return false
	}
	if !equalTriBool(a.Protected, b.Protected, false) {
		return false
	}
	if !equalOptionalBool(a.Checked, b.Checked) {
		return false
	}
	if !equalOptionalString(a.Value, b.Value) || !equalOptionalString(a.Hint, b.Hint) ||
		!equalOptionalString(a.Resource, b.Resource) {
		return false
	}
	if a.Axis != b.Axis {
		return false
	}
	if len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !equalNode(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

// equalTriBool compares a field whose absent encoding promises def.
func equalTriBool(a, b *bool, def bool) bool {
	if a == nil {
		a = &def
	}
	if b == nil {
		b = &def
	}
	return *a == *b
}

func equalOptionalBool(a, b *bool) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func equalOptionalString(a, b *string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

// sameLogicalNode reports whether two nodes have equal fields and an equal
// direct child ref list (children are compared by ref, not by content). It is
// the "did this node's own fields change" test used by the delta planner.
func sameLogicalNode(a, b *protocol.Node) bool {
	if len(a.Children) != len(b.Children) {
		return false
	}
	shallowA := *a
	shallowA.Children = nil
	shallowB := *b
	shallowB.Children = nil
	return equalNode(&shallowA, &shallowB)
}

// sameChildRefs reports whether two nodes list the same direct children in the
// same order.
func sameChildRefs(a, b *protocol.Node) bool {
	if len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if a.Children[i].Ref != b.Children[i].Ref {
			return false
		}
	}
	return true
}

// canonicalExpr is a compact, unambiguous encoding of a subtree's content
// (refs excluded). Two subtrees with the same expression are interchangeable
// for delta compression decisions.
func canonicalExpr(n *protocol.Node) string {
	buf := make([]byte, 0, 64)
	return string(appendCanonical(buf, n))
}

func appendCanonical(buf []byte, n *protocol.Node) []byte {
	if n == nil {
		return append(buf, '~')
	}
	buf = append(buf, '{')
	buf = append(buf, strconv.Quote(string(n.Role))...)
	if n.Bounds == nil {
		buf = append(buf, 'B', '-')
	} else {
		buf = append(buf, fmt.Sprintf("B%d,%d,%d,%d", n.Bounds.Left, n.Bounds.Top, n.Bounds.Right, n.Bounds.Bottom)...)
	}
	for _, label := range n.Label {
		buf = append(buf, 'L')
		buf = append(buf, strconv.Quote(label)...)
	}
	for _, action := range n.Actions {
		buf = append(buf, 'A')
		buf = append(buf, strconv.Quote(string(action))...)
	}
	buf = appendTriBool(buf, 'E', n.Enabled, true)
	buf = appendTriBool(buf, 'F', n.Focused, false)
	buf = appendTriBool(buf, 'S', n.Selected, false)
	buf = appendTriBool(buf, 'P', n.Protected, false)
	buf = appendOptBool(buf, 'C', n.Checked)
	buf = appendOptString(buf, 'V', n.Value)
	buf = appendOptString(buf, 'H', n.Hint)
	buf = appendOptString(buf, 'R', n.Resource)
	buf = append(buf, 'X')
	buf = append(buf, strconv.Quote(string(n.Axis))...)
	if n.Context {
		buf = append(buf, 'K')
	}
	if len(n.Children) == 0 {
		return append(buf, '}')
	}
	buf = append(buf, '[')
	for _, child := range n.Children {
		buf = appendCanonical(buf, child)
		buf = append(buf, ',')
	}
	return append(buf, ']')
}

func appendTriBool(buf []byte, tag byte, v *bool, def bool) []byte {
	buf = append(buf, tag)
	if v == nil {
		v = &def
	}
	if *v {
		return append(buf, '1')
	}
	return append(buf, '0')
}

func appendOptBool(buf []byte, tag byte, v *bool) []byte {
	buf = append(buf, tag)
	if v == nil {
		return append(buf, '-')
	}
	if *v {
		return append(buf, '1')
	}
	return append(buf, '0')
}

func appendOptString(buf []byte, tag byte, v *string) []byte {
	buf = append(buf, tag)
	if v == nil {
		return append(buf, '-')
	}
	return append(buf, strconv.Quote(*v)...)
}
