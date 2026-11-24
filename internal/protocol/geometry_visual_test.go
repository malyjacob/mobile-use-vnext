package protocol

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

// TestReadBoundsPointWire pins the two-integer point form next to the existing
// rectangle form and the two omission states.
func TestReadBoundsPointWire(t *testing.T) {
	point := PointBounds(30, 40)
	if point.Kind != GeometryPoint || point.Point != (Point{X: 30, Y: 40}) {
		t.Fatalf("PointBounds = %#v, want geometry point (30,40)", point)
	}
	if wire := string(marshal(t, point)); wire != "[30,40]" {
		t.Fatalf("point wire = %s, want [30,40]", wire)
	}
	var decoded ReadBounds
	if err := json.Unmarshal([]byte("[30,40]"), &decoded); err != nil {
		t.Fatalf("decode point: %v", err)
	}
	if !decoded.Equal(point) {
		t.Fatalf("decoded point = %#v, want %#v", decoded, point)
	}
	if PointBounds(30, 40).Equal(PointBounds(30, 41)) {
		t.Fatal("different points compared equal")
	}
	if PointBounds(30, 40).Equal(KnownBounds(Bounds{Left: 30, Top: 40, Right: 30, Bottom: 40})) {
		t.Fatal("point compared equal to a rectangle with the same numbers")
	}
	if PointBounds(0, 0) != (ReadBounds{Kind: GeometryPoint}) {
		t.Fatal("zero point is not the zero-value point geometry")
	}
}

// TestReadBoundsDecodeForms covers every admitted wire form and the strict
// rejections that keep the two arrays unambiguous.
func TestReadBoundsDecodeForms(t *testing.T) {
	valid := []struct {
		body string
		want ReadBounds
	}{
		{`null`, UnknownBounds()},
		{`"hidden"`, HiddenBounds()},
		{`[1,2]`, PointBounds(1, 2)},
		{`[-5,0]`, PointBounds(-5, 0)},
		{`[1,2,3,4]`, KnownBounds(Bounds{Left: 1, Top: 2, Right: 3, Bottom: 4})},
		{`[7,8,7,8]`, KnownBounds(Bounds{Left: 7, Top: 8, Right: 7, Bottom: 8})},
	}
	for _, tc := range valid {
		var decoded ReadBounds
		if err := json.Unmarshal([]byte(tc.body), &decoded); err != nil {
			t.Fatalf("decode %s: %v", tc.body, err)
		}
		if !decoded.Equal(tc.want) {
			t.Fatalf("decode %s = %#v, want %#v", tc.body, decoded, tc.want)
		}
	}
	invalid := []string{
		`nullish`, `"visible"`, `true`, `{}`, `[]`, `[1]`, `[1,2,3]`,
		`[1,2,3,4,5]`, `[1.5,2]`, `["1",2]`, `[3,4,1,2]`, `[0,0,0,-1]`,
		`[1,2.0]`, `[1e2,2]`,
	}
	for _, body := range invalid {
		var decoded ReadBounds
		if err := json.Unmarshal([]byte(body), &decoded); err == nil {
			t.Fatalf("admitted invalid bounds %s as %#v", body, decoded)
		}
	}
}

func compactTreeWithChild(role Role, bounds ReadBounds) *ReadTree {
	root := &ReadNode{Ref: "n1", Role: RoleRoot, Bounds: KnownBounds(Bounds{Left: 0, Top: 0, Right: 10, Bottom: 20})}
	root.Children = []*ReadNode{{Ref: "n2", Role: role, Bounds: bounds}}
	return &ReadTree{Format: "full", Scope: Scope, View: ViewCompact, Root: root}
}

// TestReadTreeValidateCompactGeometry pins the compact view invariant: only root
// and scroll may deliver a rectangle; every other delivered known geometry is a
// point. Hidden and unknown stay legal on any role.
func TestReadTreeValidateCompactGeometry(t *testing.T) {
	validRoot := []ReadBounds{
		KnownBounds(Bounds{Left: 0, Top: 0, Right: 10, Bottom: 20}),
		HiddenBounds(),
		UnknownBounds(),
	}
	for _, bounds := range validRoot {
		tree := compactTreeWithChild(RoleButton, PointBounds(5, 10))
		tree.Root.Bounds = bounds
		if err := tree.Validate(); err != nil {
			t.Fatalf("compact root %#v rejected: %v", bounds, err)
		}
	}
	pointRoot := compactTreeWithChild(RoleButton, PointBounds(5, 10))
	pointRoot.Root.Bounds = PointBounds(5, 10)
	if err := pointRoot.Validate(); err == nil {
		t.Fatal("compact root point was admitted")
	}
	validChildren := []struct {
		role   Role
		bounds ReadBounds
	}{
		{RoleScroll, KnownBounds(Bounds{Left: 0, Top: 0, Right: 10, Bottom: 20})},
		{RoleButton, PointBounds(5, 10)},
		{RoleInput, PointBounds(5, 10)},
		{RoleImage, HiddenBounds()},
		{RoleText, UnknownBounds()},
		{RoleGroup, HiddenBounds()},
	}
	for _, tc := range validChildren {
		if err := compactTreeWithChild(tc.role, tc.bounds).Validate(); err != nil {
			t.Fatalf("compact child %s %#v rejected: %v", tc.role, tc.bounds, err)
		}
	}
	invalidChildren := []struct {
		role   Role
		bounds ReadBounds
	}{
		{RoleButton, KnownBounds(Bounds{Left: 0, Top: 0, Right: 10, Bottom: 20})},
		{RoleText, KnownBounds(Bounds{Left: 0, Top: 0, Right: 10, Bottom: 20})},
		{RoleScroll, PointBounds(5, 10)},
		{RoleGroup, ReadBounds{Kind: GeometryKind(9)}},
	}
	for _, tc := range invalidChildren {
		if err := compactTreeWithChild(tc.role, tc.bounds).Validate(); err == nil {
			t.Fatalf("compact child %s %#v was admitted", tc.role, tc.bounds)
		}
	}
	unsupported := compactTreeWithChild(RoleButton, PointBounds(5, 10))
	unsupported.View = "expanded"
	if err := unsupported.Validate(); err == nil {
		t.Fatal("unsupported tree view was admitted")
	}
}

// TestReadTreeValidateDetailedGeometry pins the detailed view invariant: every
// geometry is a rectangle or unknown; hidden and point geometry are rejected.
func TestReadTreeValidateDetailedGeometry(t *testing.T) {
	detailedTree := func(bounds ReadBounds) *ReadTree {
		tree := compactTreeWithChild(RoleButton, KnownBounds(Bounds{Left: 1, Top: 2, Right: 3, Bottom: 4}))
		tree.View = ViewDetailed
		tree.Root.Children[0].Bounds = bounds
		return tree
	}
	if err := detailedTree(KnownBounds(Bounds{Left: 1, Top: 2, Right: 3, Bottom: 4})).Validate(); err != nil {
		t.Fatalf("detailed rectangle rejected: %v", err)
	}
	if err := detailedTree(UnknownBounds()).Validate(); err != nil {
		t.Fatalf("detailed unknown geometry rejected: %v", err)
	}
	for _, bounds := range []ReadBounds{
		PointBounds(2, 3), HiddenBounds(), ReadBounds{Kind: GeometryKind(9)},
	} {
		if err := detailedTree(bounds).Validate(); err == nil {
			t.Fatalf("detailed %#v was admitted", bounds)
		}
	}
}

// TestReadTreePointGeometryRoundTrip keeps a compact tree with all four geometry
// states lossless across the canonical wire encoding.
func TestReadTreePointGeometryRoundTrip(t *testing.T) {
	root := testReadNode("n1", RoleRoot)
	button := testReadNode("n2", RoleButton)
	button.Bounds = PointBounds(30, 40)
	button.Label = []string{"确定"}
	button.Actions = []Action{ActionTap}
	scroll := testReadNode("n3", RoleScroll)
	scroll.Bounds = KnownBounds(Bounds{Left: 0, Top: 50, Right: 100, Bottom: 200})
	text := testReadNode("n4", RoleText)
	text.Bounds = HiddenBounds()
	text.Label = []string{"标题"}
	image := testReadNode("n5", RoleImage)
	image.Bounds = UnknownBounds()
	root.Children = []*ReadNode{button, scroll, text, image}
	tree := &ReadTree{Format: "full", Scope: Scope, View: ViewCompact, Root: root}
	if err := tree.Validate(); err != nil {
		t.Fatalf("fixture tree is invalid: %v", err)
	}
	decoded := roundTripDecode(t, tree)
	if !decoded.Root.Children[0].Bounds.Equal(button.Bounds) ||
		!decoded.Root.Children[2].Bounds.Equal(HiddenBounds()) ||
		!decoded.Root.Children[3].Bounds.Equal(UnknownBounds()) {
		t.Fatalf("geometry did not round trip: %#v", decoded.Root.Children)
	}
}

// TestReadSetNodePointBoundsRoundTrip keeps the point form of a delta set value
// explicit on the wire and lossless on decode.
func TestReadSetNodePointBoundsRoundTrip(t *testing.T) {
	delta := &ReadDelta{
		Format: "delta", Base: "O1", View: ViewCompact,
		Set: map[string]ReadSetNode{
			"n2": {Bounds: Field[ReadBounds]{Set: true, Value: PointBounds(10, 20)}},
		},
	}
	wire := string(marshal(t, delta))
	if !strings.Contains(wire, `"bounds":[10,20]`) {
		t.Fatalf("delta set wire = %s, want point bounds", wire)
	}
	var decoded ReadDelta
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	set := decoded.Set["n2"]
	if !set.Bounds.Set || !set.Bounds.Value.Equal(PointBounds(10, 20)) {
		t.Fatalf("decoded set bounds = %#v, want point (10,20)", set.Bounds)
	}
}

// TestObservationVisualWire pins the optional visual member: it is emitted after
// foreground, omitted when empty, and never null or empty on decode.
func TestObservationVisualWire(t *testing.T) {
	observation := &Observation{
		Status:     "complete",
		Foreground: &Foreground{State: "none"},
		Visual:     "sparse_tree",
	}
	wire := string(marshal(t, observation))
	const want = `{"status":"complete","foreground":{"state":"none"},"visual":"sparse_tree"}`
	if wire != want {
		t.Fatalf("observation wire = %s, want %s", wire, want)
	}
	var decoded Observation
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatalf("decode observation: %v", err)
	}
	if decoded.Visual != "sparse_tree" {
		t.Fatalf("decoded visual = %q, want sparse_tree", decoded.Visual)
	}

	empty := &Observation{Status: "complete"}
	if wire := string(marshal(t, empty)); strings.Contains(wire, "visual") {
		t.Fatalf("empty visual was encoded: %s", wire)
	}
	for _, body := range []string{
		`{"status":"complete","visual":null}`,
		`{"status":"complete","visual":""}`,
		`{"status":"complete","visual":1}`,
	} {
		var observation Observation
		if err := json.Unmarshal([]byte(body), &observation); err == nil {
			t.Fatalf("admitted invalid visual %s", body)
		}
	}
}
