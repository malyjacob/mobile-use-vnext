package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func testBounds(left, top, right, bottom int) *protocol.Bounds {
	return &protocol.Bounds{Left: left, Top: top, Right: right, Bottom: bottom}
}

func testNode(ref string, role protocol.Role, bounds *protocol.Bounds, context bool,
	children ...*protocol.Node) *protocol.Node {
	return &protocol.Node{Ref: ref, Role: role, Bounds: bounds, Context: context, Children: children}
}

func TestResolvePointStates(t *testing.T) {
	tests := []struct {
		name  string
		tree  *protocol.Node
		x, y  int
		want  string
		state pointState
	}{
		{
			name: "unique deepest child",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleGroup, testBounds(0, 0, 100, 100), false,
					testNode("n2", protocol.RoleButton, testBounds(10, 10, 50, 50), false))),
			x: 20, y: 20, want: "n2", state: pointUnique,
		},
		{
			name: "parent wins when the child does not contain the point",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleGroup, testBounds(0, 0, 100, 100), false,
					testNode("n2", protocol.RoleButton, testBounds(10, 10, 50, 50), false))),
			x: 80, y: 80, want: "n1", state: pointUnique,
		},
		{
			name: "root only is none",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false),
			x:    20, y: 20, want: "", state: pointNone,
		},
		{
			name: "outside every node is none",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleButton, testBounds(10, 10, 50, 50), false)),
			x: 200, y: 200, want: "", state: pointNone,
		},
		{
			name: "nil bounds are not candidates",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleGroup, nil, false)),
			x: 20, y: 20, want: "", state: pointNone,
		},
		{
			name: "context is not a candidate",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleGroup, testBounds(0, 0, 100, 100), true)),
			x: 20, y: 20, want: "", state: pointNone,
		},
		{
			name: "published descendant of context still resolves",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleGroup, testBounds(0, 0, 100, 100), true,
					testNode("n2", protocol.RoleText, testBounds(10, 10, 50, 50), false))),
			x: 20, y: 20, want: "n2", state: pointUnique,
		},
		{
			name: "nested root is not a candidate",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleRoot, testBounds(0, 0, 100, 100), false)),
			x: 20, y: 20, want: "", state: pointNone,
		},
		{
			name: "two same-depth siblings are ambiguous",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleButton, testBounds(0, 0, 60, 60), false),
				testNode("n2", protocol.RoleButton, testBounds(40, 40, 100, 100), false)),
			x: 50, y: 50, want: "", state: pointAmbiguous,
		},
		{
			name: "ambiguity is only at the deepest level",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleButton, testBounds(0, 0, 60, 60), false),
				testNode("n2", protocol.RoleGroup, testBounds(0, 0, 100, 100), false,
					testNode("n3", protocol.RoleText, testBounds(10, 10, 20, 20), false))),
			x: 15, y: 15, want: "n3", state: pointUnique,
		},
		{
			name: "distinct branches at the same depth can be ambiguous",
			tree: testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
				testNode("n1", protocol.RoleGroup, testBounds(0, 0, 100, 50), false,
					testNode("n2", protocol.RoleText, testBounds(0, 0, 50, 50), false)),
				testNode("n3", protocol.RoleGroup, testBounds(0, 0, 100, 100), false,
					testNode("n4", protocol.RoleText, testBounds(40, 0, 100, 50), false))),
			x: 45, y: 25, want: "", state: pointAmbiguous,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ref, state := resolvePoint(testCase.tree, testCase.x, testCase.y)
			if ref != testCase.want || state != testCase.state {
				t.Fatalf("resolvePoint = (%q, %s), want (%q, %s)",
					ref, state, testCase.want, testCase.state)
			}
		})
	}
}

func TestResolvePointHalfOpenBounds(t *testing.T) {
	tree := testNode("n0", protocol.RoleRoot, testBounds(0, 0, 100, 100), false,
		testNode("n1", protocol.RoleButton, testBounds(0, 0, 10, 10), false))
	for _, point := range [][2]int{{0, 0}, {9, 9}} {
		ref, state := resolvePoint(tree, point[0], point[1])
		if ref != "n1" || state != pointUnique {
			t.Fatalf("point %v inside half-open bounds = (%q, %s)", point, ref, state)
		}
	}
	for _, point := range [][2]int{{10, 9}, {9, 10}, {10, 10}} {
		ref, state := resolvePoint(tree, point[0], point[1])
		if ref != "" || state != pointNone {
			t.Fatalf("point %v outside half-open bounds = (%q, %s)", point, ref, state)
		}
	}
}

func TestResolvePointNilTree(t *testing.T) {
	if ref, state := resolvePoint(nil, 1, 1); ref != "" || state != pointNone {
		t.Fatalf("nil tree = (%q, %s), want none", ref, state)
	}
}

func TestCoordinatePoint(t *testing.T) {
	tests := []struct {
		name      string
		operation *protocol.Operation
		x, y      int
		ok        bool
	}{
		{name: "tap coordinate", operation: &protocol.Operation{
			Type: "tap", Target: &protocol.Target{X: 3, Y: 4}}, x: 3, y: 4, ok: true},
		{name: "tap ref", operation: &protocol.Operation{
			Type: "tap", Target: &protocol.Target{UseRef: true, Ref: "n1"}}},
		{name: "long press coordinate", operation: &protocol.Operation{
			Type: "long_press", Target: &protocol.Target{X: 7, Y: 8}}, x: 7, y: 8, ok: true},
		{name: "text coordinate", operation: &protocol.Operation{
			Type: "text", Target: &protocol.Target{X: 9, Y: 10}}, x: 9, y: 10, ok: true},
		{name: "swipe proves from", operation: &protocol.Operation{
			Type: "swipe", From: &protocol.Point{X: 11, Y: 12}, To: &protocol.Point{X: 13, Y: 14}},
			x: 11, y: 12, ok: true},
		{name: "key has no point", operation: &protocol.Operation{Type: "key", Key: "BACK"}},
		{name: "missing target", operation: &protocol.Operation{Type: "tap"}},
		{name: "nil operation"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			x, y, ok := coordinatePoint(testCase.operation)
			if ok != testCase.ok || x != testCase.x || y != testCase.y {
				t.Fatalf("coordinatePoint = (%d, %d, %v), want (%d, %d, %v)",
					x, y, ok, testCase.x, testCase.y, testCase.ok)
			}
		})
	}
}
