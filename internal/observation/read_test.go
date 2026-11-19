package observation

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The read-layer tests use synthetic trees only. They prove the fixed public
// projection rules: the checked default, the three geometry states, the
// anonymous-plain predicate, chain folding and the public delta round trip.

func readBool(value bool) *bool { return &value }

func readString(value string) *string { return &value }

func readRect(left, top, right, bottom int) protocol.ReadBounds {
	return protocol.KnownBounds(protocol.Bounds{Left: left, Top: top, Right: right, Bottom: bottom})
}

// plainT returns one internal node in the known default state of an anonymous
// plain view: enabled=true, focused/selected/protected/checked=false.
func plainT(ref string, role protocol.Role) *protocol.Node {
	return &protocol.Node{
		Ref: ref, Role: role,
		Bounds:  &protocol.Bounds{Left: 0, Top: 0, Right: 100, Bottom: 100},
		Label:   []string{},
		Actions: []protocol.Action{},
		Enabled: readBool(true), Focused: readBool(false), Selected: readBool(false),
		Protected: readBool(false), Checked: readBool(false),
		Children: []*protocol.Node{},
	}
}

func rootT(children ...*protocol.Node) *protocol.Tree {
	root := plainT("n1", protocol.RoleRoot)
	root.Bounds = &protocol.Bounds{Left: 0, Top: 0, Right: 400, Bottom: 800}
	root.Children = children
	return &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
}

func childOf(t *testing.T, node *protocol.ReadNode, ref string) *protocol.ReadNode {
	t.Helper()
	if node.Ref == ref {
		return node
	}
	for _, child := range node.Children {
		if found := childOf(t, child, ref); found != nil {
			return found
		}
	}
	return nil
}

func TestProjectCompactCheckedDefault(t *testing.T) {
	unknown := plainT("n2", protocol.RoleText)
	unknown.Checked = nil
	unknown.Label = []string{"标题"}
	checked := plainT("n3", protocol.RoleCheckbox)
	checked.Checked = readBool(true)
	plain := plainT("n4", protocol.RoleGroup)
	tree := ProjectCompactTree(rootT(unknown, checked, plain))

	encoded, err := protocol.JSON(tree)
	if err != nil {
		t.Fatalf("encode compact tree: %v", err)
	}
	text := string(encoded)
	if strings.Contains(text, `"defaults"`) {
		t.Fatalf("compact tree must not carry defaults: %s", text)
	}
	if strings.Contains(text, `"ref":"n4","role":"group","checked"`) ||
		strings.Contains(text, `"ref":"n4","role":"group","bounds"`) {
		t.Fatalf("known false checked must be omitted: %s", text)
	}
	if !strings.Contains(text, `"ref":"n2","role":"text"`) || !strings.Contains(text, `"checked":null`) {
		t.Fatalf("unknown checked must be explicit null: %s", text)
	}
	if !strings.Contains(text, `"checked":true`) {
		t.Fatalf("known true checked must be kept: %s", text)
	}
	// Decoding restores the default instead of inventing an unknown state.
	var back protocol.ReadTree
	if err := protocolDecode(encoded, &back); err != nil {
		t.Fatalf("decode compact tree: %v", err)
	}
	group := childOf(t, back.Root, "n4")
	if group == nil || group.Checked == nil || *group.Checked {
		t.Fatalf("absent checked must decode as known false: %+v", group)
	}
	text2 := childOf(t, back.Root, "n2")
	if text2 == nil || text2.Checked != nil {
		t.Fatalf("explicit null must decode as unknown: %+v", text2)
	}
}

func TestReadSetPreservesExplicitValues(t *testing.T) {
	yes, no := true, false
	empty := ""
	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			"n2": {
				Checked: protocol.Field[*bool]{Set: true, Value: &no},
				Label:   protocol.Field[[]string]{Set: true, Value: []string{}},
				Value:   protocol.Field[*string]{Set: true, Value: &empty},
				Bounds:  protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.UnknownBounds()},
			},
			"n3": {
				Checked: protocol.Field[*bool]{Set: true},
				Focused: protocol.Field[*bool]{Set: true, Value: &yes},
				Bounds:  protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.HiddenBounds()},
			},
		},
	}
	encoded, err := protocol.JSON(delta)
	if err != nil {
		t.Fatalf("encode delta: %v", err)
	}
	for _, want := range []string{
		`"checked":false`, `"label":[]`, `"value":""`, `"bounds":null`,
		`"checked":null`, `"focused":true`, `"bounds":"hidden"`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("delta lost %s: %s", want, encoded)
		}
	}
	var back protocol.ReadDelta
	if err := protocolDecode(encoded, &back); err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	if !back.Set["n2"].Checked.Set || back.Set["n2"].Checked.Value == nil || *back.Set["n2"].Checked.Value {
		t.Fatal("explicit checked=false lost")
	}
	if back.Set["n3"].Bounds.Value.Kind != protocol.GeometryHidden {
		t.Fatal("explicit hidden bounds lost")
	}
	if back.Set["n2"].Bounds.Value.Kind != protocol.GeometryUnknown {
		t.Fatal("explicit unknown bounds lost")
	}
}

func TestReadDeltaCombinedSetAndEmptyOmitDefaults(t *testing.T) {
	baseLeaf := plainT("n2", protocol.RoleInput)
	baseLeaf.Value = readString("hello")
	baseLeaf.Label = []string{"标签"}
	baseLeaf.Checked = nil
	base := ProjectCompactTree(rootT(baseLeaf))

	emptyDelta, err := ReadDiff("O1", base, base)
	if err != nil {
		t.Fatalf("empty ReadDiff: %v", err)
	}
	emptyJSON, err := protocol.JSON(emptyDelta)
	if err != nil {
		t.Fatalf("protocol.JSON(emptyDelta): %v", err)
	}
	if string(emptyJSON) != `{"format":"delta","base":"O1","view":"compact"}` {
		t.Fatalf("empty delta JSON = %s", emptyJSON)
	}

	curLeaf := plainT("n2", protocol.RoleInput)
	emptyStr := ""
	curLeaf.Value = &emptyStr
	curLeaf.Label = []string{}
	curLeaf.Checked = readBool(false)
	curLeaf.Focused = readBool(true)
	curLeaf.Enabled = nil
	current := ProjectCompactTree(rootT(curLeaf))

	delta, err := ReadDiff("O1", base, current)
	if err != nil {
		t.Fatalf("ReadDiff: %v", err)
	}
	deltaJSON, err := protocol.JSON(delta)
	if err != nil {
		t.Fatalf("protocol.JSON(delta): %v", err)
	}
	for _, want := range []string{`"checked":false`, `"focused":true`, `"enabled":null`, `"value":""`, `"label":[]`} {
		if !strings.Contains(string(deltaJSON), want) {
			t.Fatalf("delta JSON lost %s: %s", want, deltaJSON)
		}
	}
	if strings.Contains(string(deltaJSON), `"defaults"`) {
		t.Fatalf("delta JSON must not include defaults: %s", deltaJSON)
	}
	applied, err := ReadApply(base, delta)
	if err != nil {
		t.Fatalf("ReadApply: %v", err)
	}
	if !ReadEqual(applied, current) {
		t.Fatalf("ReadApply mismatch")
	}
	gotBytes, err := protocol.JSON(applied)
	if err != nil {
		t.Fatalf("protocol.JSON(applied): %v", err)
	}
	wantBytes, err := protocol.JSON(current)
	if err != nil {
		t.Fatalf("protocol.JSON(current): %v", err)
	}
	if string(gotBytes) != string(wantBytes) {
		t.Fatalf("canonical JSON mismatch:\ngot:  %s\nwant: %s", gotBytes, wantBytes)
	}
}

func TestUnknownStateNodesKeepBoundsAndStayOutOfChains(t *testing.T) {
	unknownChild := plainT("n3", protocol.RoleView)
	unknownChild.Selected = nil
	unknownParent := plainT("n2", protocol.RoleView)
	unknownParent.Protected = nil
	unknownParent.Children = []*protocol.Node{unknownChild}
	leaf := plainT("n4", protocol.RoleText)
	leaf.Label = []string{"保留"}
	unknownChild.Children = []*protocol.Node{leaf}

	compact := ProjectCompactTree(rootT(unknownParent))
	if hasChain(compact.Root, []string{"n2", "n3"}) {
		t.Fatalf("unknown-state nodes must not join a chain: %+v", compact.Root)
	}
	// The unknown-state ancestor keeps its distinct rectangle; the descendant
	// with the same clipped rectangle is a duplicate and hides it.
	if node := childOf(t, compact.Root, "n2"); node == nil || node.Bounds.Kind != protocol.GeometryKnown {
		t.Fatalf("unknown-state ancestor n2 must keep known geometry: %+v", node)
	}
	if node := childOf(t, compact.Root, "n3"); node == nil || node.Bounds.Kind != protocol.GeometryHidden {
		t.Fatalf("duplicate unknown-state child n3 must hide its geometry: %+v", node)
	}
	if node := childOf(t, compact.Root, "n4"); node == nil || node.Bounds.Kind != protocol.GeometryHidden {
		t.Fatalf("actionless text n4 must hide its geometry: %+v", node)
	}

	// Same packing guard applies to add/replace delta definitions.
	delta := PackDelta(&protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Add: []*protocol.ReadNode{projectRead(unknownParent)},
		Replace: map[string]*protocol.ReadNode{
			"n2": projectRead(unknownParent),
		},
	})
	if hasChain(delta.Add[0], []string{"n2", "n3"}) {
		t.Fatalf("PackDelta must not chain unknown-state add nodes: %+v", delta.Add[0])
	}
	if hasChain(delta.Replace["n2"], []string{"n2", "n3"}) {
		t.Fatalf("PackDelta must not chain unknown-state replace nodes: %+v", delta.Replace["n2"])
	}
}

func TestGeometryStatesAreDistinct(t *testing.T) {
	unknown := plainT("n2", protocol.RoleView)
	unknown.Bounds = nil
	anonymous := plainT("n3", protocol.RoleGroup)
	functional := plainT("n4", protocol.RoleButton)
	functional.Actions = []protocol.Action{protocol.ActionTap}
	functional.Bounds = &protocol.Bounds{Left: 1, Top: 2, Right: 3, Bottom: 4}
	tree := ProjectCompactTree(rootT(unknown, anonymous, functional))

	if node := childOf(t, tree.Root, "n2"); node == nil || node.Bounds.Kind != protocol.GeometryUnknown {
		t.Fatalf("missing source geometry must stay unknown: %+v", node)
	}
	if node := childOf(t, tree.Root, "n3"); node == nil || node.Bounds.Kind != protocol.GeometryHidden {
		t.Fatalf("anonymous plain geometry must be hidden: %+v", node)
	}
	if node := childOf(t, tree.Root, "n4"); node == nil || node.Bounds.Kind != protocol.GeometryKnown {
		t.Fatalf("functional geometry must be delivered: %+v", node)
	}
	detailed := ProjectDetailedTree(rootT(unknown, anonymous, functional))
	for _, ref := range []string{"n2", "n3", "n4"} {
		node := childOf(t, detailed.Root, ref)
		if node == nil || node.Bounds.Kind == protocol.GeometryHidden {
			t.Fatalf("detailed view must not hide %s: %+v", ref, node)
		}
	}
	if err := detailed.Validate(); err != nil {
		t.Fatalf("detailed tree must validate: %v", err)
	}
}

func TestAnonymousPlainPredicate(t *testing.T) {
	base := plainT("n2", protocol.RoleView)
	if !AnonymousPlain(projectRead(base)) {
		t.Fatal("a known-default view must be anonymous plain")
	}
	cases := map[string]func(n *protocol.Node){
		"root role":         func(n *protocol.Node) { n.Role = protocol.RoleRoot },
		"context":           func(n *protocol.Node) { n.Context = true },
		"action":            func(n *protocol.Node) { n.Actions = []protocol.Action{protocol.ActionTap} },
		"label":             func(n *protocol.Node) { n.Label = []string{"x"} },
		"empty value":       func(n *protocol.Node) { n.Value = readString("") },
		"empty hint":        func(n *protocol.Node) { n.Hint = readString("") },
		"empty resource":    func(n *protocol.Node) { n.Resource = readString("") },
		"axis":              func(n *protocol.Node) { n.Axis = protocol.AxisY },
		"unknown enabled":   func(n *protocol.Node) { n.Enabled = nil },
		"disabled":          func(n *protocol.Node) { n.Enabled = readBool(false) },
		"unknown focused":   func(n *protocol.Node) { n.Focused = nil },
		"unknown checked":   func(n *protocol.Node) { n.Checked = nil },
		"unknown selected":  func(n *protocol.Node) { n.Selected = nil },
		"unknown protected": func(n *protocol.Node) { n.Protected = nil },
		"selected":          func(n *protocol.Node) { n.Selected = readBool(true) },
		"protected":         func(n *protocol.Node) { n.Protected = readBool(true) },
		"input role":        func(n *protocol.Node) { n.Role = protocol.RoleInput },
		"scroll role":       func(n *protocol.Node) { n.Role = protocol.RoleScroll },
		"image role":        func(n *protocol.Node) { n.Role = protocol.RoleImage },
		"text role":         func(n *protocol.Node) { n.Role = protocol.RoleText },
		"checkable role":    func(n *protocol.Node) { n.Role = protocol.RoleCheckbox },
	}
	for name, mutate := range cases {
		node := plainT("n2", protocol.RoleView)
		mutate(node)
		if AnonymousPlain(projectRead(node)) {
			t.Fatalf("%s must not satisfy the anonymous plain predicate", name)
		}
	}
}

// projectRead projects one internal node in the compact view.
func projectRead(node *protocol.Node) *protocol.ReadNode {
	return projectReadNode(node, true)
}

func TestPackFoldsOnlyLegalChains(t *testing.T) {
	// n2 -> n3 -> n4 is an anonymous single-child path; n5 is a sibling and
	// n6 splits the path by carrying an action.
	leaf := plainT("n4", protocol.RoleView)
	mid := plainT("n3", protocol.RoleView)
	mid.Children = []*protocol.Node{leaf}
	head := plainT("n2", protocol.RoleView)
	head.Children = []*protocol.Node{mid}
	sibling := plainT("n8", protocol.RoleGroup)
	action := plainT("n6", protocol.RoleButton)
	action.Actions = []protocol.Action{protocol.ActionTap}
	action.Label = []string{"确定"}
	deep := plainT("n7", protocol.RoleGroup) // child of the action node
	action.Children = []*protocol.Node{deep}
	root := rootT(head, sibling, action)
	compact := ProjectCompactTree(root)

	if !hasChain(compact.Root, []string{"n2", "n3", "n4"}) {
		t.Fatalf("expected the anonymous single-child path to fold: %+v", compact.Root)
	}
	if hasChain(compact.Root, []string{"n2", "n3", "n4", "n8"}) || hasChain(compact.Root, []string{"n6", "n7"}) {
		t.Fatal("a sibling or a functional node must not join a chain")
	}
	// Definition roots stay ordinary anchors and every ref survives.
	if compact.Root.IsChain() {
		t.Fatal("the tree root must stay one ordinary definition anchor")
	}
	var refs []string
	collectReadRefs(compact.Root, &refs)
	want := []string{"n1", "n2", "n3", "n4", "n8", "n6", "n7"}
	if strings.Join(refs, ",") != strings.Join(want, ",") {
		t.Fatalf("packing changed the logical refs: got %v want %v", refs, want)
	}
	// Unpacking restores the exact logical tree.
	logical := UnpackTree(compact)
	if !ReadEqual(logical, UnpackTree(ProjectCompactTree(root))) {
		t.Fatal("unpack is not the inverse of pack")
	}
}

func TestPackIsDeterministic(t *testing.T) {
	build := func() *protocol.Tree {
		leaf := plainT("n4", protocol.RoleView)
		mid := plainT("n3", protocol.RoleView)
		mid.Children = []*protocol.Node{leaf}
		head := plainT("n2", protocol.RoleView)
		head.Children = []*protocol.Node{mid}
		return rootT(head)
	}
	first, err := protocol.JSON(ProjectCompactTree(build()))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	second, err := protocol.JSON(ProjectCompactTree(build()))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("projection is not deterministic:\n%s\n%s", first, second)
	}
}

func TestChainMembersWithDifferentInternalRects(t *testing.T) {
	leaf := plainT("n4", protocol.RoleView)
	leaf.Bounds = &protocol.Bounds{Left: 10, Top: 10, Right: 90, Bottom: 90}
	mid := plainT("n3", protocol.RoleView)
	mid.Bounds = &protocol.Bounds{Left: 5, Top: 5, Right: 95, Bottom: 95}
	mid.Children = []*protocol.Node{leaf}
	head := plainT("n2", protocol.RoleView)
	head.Bounds = &protocol.Bounds{Left: 0, Top: 0, Right: 100, Bottom: 100}
	head.Children = []*protocol.Node{mid}
	compact := ProjectCompactTree(rootT(head))
	if !hasChain(compact.Root, []string{"n2", "n3", "n4"}) {
		t.Fatal("nodes whose public geometry is hidden may share a chain")
	}
	detailed := ProjectDetailedTree(rootT(head))
	if node := childOf(t, detailed.Root, "n3"); node == nil ||
		node.Bounds.Kind != protocol.GeometryKnown || node.Bounds.Rect.Left != 5 {
		t.Fatalf("detailed view must restore each own rectangle: %+v", node)
	}
}

func TestReadDeltaFollowsDisplayStateChanges(t *testing.T) {
	// A chain member gains an action without changing its internal rectangle:
	// the public delta must add the action and publish the now-visible geometry.
	before := plainT("n2", protocol.RoleView)
	before.Children = []*protocol.Node{plainT("n3", protocol.RoleView)}
	base := ProjectCompactTree(rootT(before))

	after := plainT("n2", protocol.RoleView)
	button := plainT("n3", protocol.RoleView)
	button.Actions = []protocol.Action{protocol.ActionTap}
	after.Children = []*protocol.Node{button}
	current := ProjectCompactTree(rootT(after))

	delta, err := ReadDiff("O1", base, current)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	set, ok := delta.Set["n3"]
	if !ok || !set.Actions.Set || len(set.Actions.Value) != 1 {
		t.Fatalf("expected an action set on n3: %+v", delta.Set)
	}
	if !set.Bounds.Set || set.Bounds.Value.Kind != protocol.GeometryKnown {
		t.Fatalf("new visible geometry must be published: %+v", set.Bounds)
	}

	// Back to the anonymous plain state: actions=[] and bounds=hidden.
	back, err := ReadDiff("O2", current, base)
	if err != nil {
		t.Fatalf("reverse diff: %v", err)
	}
	set, ok = back.Set["n3"]
	if !ok || !set.Actions.Set || len(set.Actions.Value) != 0 {
		t.Fatalf("expected actions=[] on n3: %+v", back.Set)
	}
	if !set.Bounds.Set || set.Bounds.Value.Kind != protocol.GeometryHidden {
		t.Fatalf("hidden geometry must be published as bounds=hidden: %+v", set.Bounds)
	}
}

func TestReadDeltaChildrenReferenceChainMember(t *testing.T) {
	first := plainT("n2", protocol.RoleView)
	first.Children = []*protocol.Node{plainT("n3", protocol.RoleView)}
	sibling := plainT("n4", protocol.RoleView)
	base := ProjectCompactTree(rootT(first, sibling))

	// Move n3 from beneath n2 to the root child list. The ref belongs to a chain
	// member in the baseline; the delta must address it by its original ref.
	moved := plainT("n3", protocol.RoleView)
	reordered := rootT(plainT("n2", protocol.RoleView), moved, sibling)
	current := ProjectCompactTree(reordered)

	delta, err := ReadDiff("O1", base, current)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	rebuilt, err := ReadApply(base, delta)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !ReadEqual(rebuilt, current) {
		t.Fatalf("apply(diff) differs from the current read tree")
	}
	if _, ok := delta.Children["n1"]; !ok {
		t.Fatalf("expected an explicit children block for the root: %+v", delta.Children)
	}
}

func TestReadDeltaSameBaselineReplacesPreviousDelta(t *testing.T) {
	leaf := plainT("n2", protocol.RoleView)
	leaf.Checked = readBool(false)
	base := ProjectCompactTree(rootT(leaf))

	first := plainT("n2", protocol.RoleView)
	first.Checked = readBool(true)
	firstTree := ProjectCompactTree(rootT(first))
	d1, err := ReadDiff("O1", base, firstTree)
	if err != nil {
		t.Fatalf("d1: %v", err)
	}
	if !d1.Set["n2"].Checked.Set || !*d1.Set["n2"].Checked.Value {
		t.Fatalf("d1 must set checked=true: %+v", d1.Set)
	}

	// A later delta against the same baseline omits the field, so applying it
	// must restore the baseline value rather than inherit d1.
	d2, err := ReadDiff("O1", base, base)
	if err != nil {
		t.Fatalf("d2: %v", err)
	}
	if !d2.IsEmpty() {
		t.Fatalf("an identical tree must produce an empty delta: %+v", d2)
	}
	rebuilt, err := ReadApply(base, d2)
	if err != nil {
		t.Fatalf("apply d2: %v", err)
	}
	if !ReadEqual(rebuilt, base) {
		t.Fatal("d2 must restore the baseline value")
	}
}

func TestReadDeltaRejectsRoleChange(t *testing.T) {
	leaf := plainT("n2", protocol.RoleView)
	base := ProjectCompactTree(rootT(leaf))
	changed := plainT("n2", protocol.RoleGroup)
	current := ProjectCompactTree(rootT(changed))
	if _, err := ReadDiff("O1", base, current); err == nil {
		t.Fatal("a role change has no legal delta")
	}
}

func TestCompactProjectionReducesRedundantEncoding(t *testing.T) {
	// Thirty nested anonymous groups and a hundred known-false anonymous leaves
	// inside the fixed depth limit.
	var chain *protocol.Node
	for i := 30; i >= 2; i-- {
		node := plainT("n"+itoa(i), protocol.RoleGroup)
		if chain != nil {
			node.Children = []*protocol.Node{chain}
		}
		chain = node
	}
	root := rootT(chain)
	for i := 0; i < 100; i++ {
		root.Root.Children = append(root.Root.Children, plainT("m"+itoa(i+1), protocol.RoleView))
	}
	oldBytes, err := protocol.JSON(root)
	if err != nil {
		t.Fatalf("encode internal tree: %v", err)
	}
	newBytes, err := protocol.JSON(ProjectCompactTree(root))
	if err != nil {
		t.Fatalf("encode compact tree: %v", err)
	}
	if len(newBytes)*4 > len(oldBytes)*3 {
		t.Fatalf("compact encoding is not at least 25%% smaller: old=%d new=%d",
			len(oldBytes), len(newBytes))
	}
	// The detailed view keeps every rectangle and is larger than the compact one.
	detailed, err := protocol.JSON(ProjectDetailedTree(root))
	if err != nil {
		t.Fatalf("encode detailed tree: %v", err)
	}
	if len(detailed) <= len(newBytes) {
		t.Fatal("detailed view must not be smaller than the compact view")
	}
}

func TestSnapshotCostIsDeterministic(t *testing.T) {
	tree := rootT(plainT("n2", protocol.RoleView))
	capture := &device.Capture{Display: device.Display{ID: 1, Width: 400, Height: 800},
		Root: 0, Nodes: []device.RawNode{{Index: 0, Parent: -1}}}
	first, err := SnapshotCost(capture, tree)
	if err != nil {
		t.Fatalf("cost: %v", err)
	}
	second, err := SnapshotCost(capture, tree)
	if err != nil {
		t.Fatalf("cost: %v", err)
	}
	if first <= 0 || first != second {
		t.Fatalf("snapshot cost must be repeatable: %d %d", first, second)
	}
}

func hasChain(root *protocol.ReadNode, refs []string) bool {
	if root == nil {
		return false
	}
	if strings.Join(root.Chain, ",") == strings.Join(refs, ",") {
		return true
	}
	for _, child := range root.Children {
		if hasChain(child, refs) {
			return true
		}
	}
	return false
}

func collectReadRefs(node *protocol.ReadNode, out *[]string) {
	if node == nil {
		return
	}
	*out = append(*out, node.Refs()...)
	for _, child := range node.Children {
		collectReadRefs(child, out)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// protocolDecode decodes one published read value through the same strict
// decoder the HTTP client uses.
func protocolDecode(data []byte, value any) error {
	return protocol.Decode(data, value)
}

func readR(ref string, role protocol.Role, label string, children ...*protocol.ReadNode) *protocol.ReadNode {
	return &protocol.ReadNode{
		Ref: ref, Role: role,
		Bounds:    readRect(0, 0, 100, 100),
		Label:     []string{label},
		Actions:   []protocol.Action{},
		Enabled:   readBool(true),
		Focused:   readBool(false),
		Selected:  readBool(false),
		Checked:   readBool(false),
		Protected: readBool(false),
		Children:  children,
	}
}

func readRootR(children ...*protocol.ReadNode) *protocol.ReadTree {
	return PackTree(&protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact,
		Root: readR("n1", protocol.RoleRoot, "root", children...),
	})
}

func mustReadDelta(t *testing.T, baseID string, base, current *protocol.ReadTree) *protocol.ReadDelta {
	t.Helper()
	delta, err := ReadDiff(baseID, base, current)
	if err != nil {
		t.Fatalf("ReadDiff: %v", err)
	}
	return delta
}

func mustReadApplyEqual(t *testing.T, base *protocol.ReadTree, delta *protocol.ReadDelta, current *protocol.ReadTree) {
	t.Helper()
	got, err := ReadApply(base, delta)
	if err != nil {
		t.Fatalf("ReadApply: %v", err)
	}
	if !ReadEqual(got, current) {
		t.Fatalf("ReadApply result differs from current")
	}
	gotJSON, err := protocol.JSON(got)
	if err != nil {
		t.Fatalf("encode applied tree: %v", err)
	}
	wantJSON, err := protocol.JSON(current)
	if err != nil {
		t.Fatalf("encode current tree: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("canonical bytes differ:\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}

func requireReadChildBlock(t *testing.T, delta *protocol.ReadDelta, parent string, want ...string) {
	t.Helper()
	got, ok := delta.Children[parent]
	if !ok {
		t.Fatalf("missing children[%s]: %+v", parent, delta.Children)
	}
	if got == nil {
		t.Fatalf("children[%s] must be an explicit list, not nil", parent)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("children[%s] = %v, want %v", parent, got, want)
	}
}

func readDefinitionRefs(defs []*protocol.ReadNode) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		if def != nil {
			out = append(out, def.Ref)
		}
	}
	return out
}

func readWireText(t *testing.T, value any) string {
	t.Helper()
	encoded, err := protocol.JSON(value)
	if err != nil {
		t.Fatalf("protocol.JSON: %v", err)
	}
	return string(encoded)
}

func requireReadRejectedAtomically(t *testing.T, base *protocol.ReadTree, delta *protocol.ReadDelta) {
	t.Helper()
	var baseBefore protocol.ReadTree
	if err := protocol.Decode([]byte(readWireText(t, base)), &baseBefore); err != nil {
		t.Fatalf("clone baseline: %v", err)
	}
	var deltaBefore protocol.ReadDelta
	if err := protocol.Decode([]byte(readWireText(t, delta)), &deltaBefore); err != nil {
		t.Fatalf("clone delta: %v", err)
	}
	got, err := ReadApply(base, delta)
	if err == nil {
		t.Fatalf("ReadApply accepted an illegal delta: %+v", got)
	}
	if got != nil {
		t.Fatalf("ReadApply returned a partial tree on rejection")
	}
	var baseAfter protocol.ReadTree
	if err := protocol.Decode([]byte(readWireText(t, base)), &baseAfter); err != nil {
		t.Fatalf("read baseline after rejection: %v", err)
	}
	if !reflect.DeepEqual(baseAfter, baseBefore) {
		t.Fatalf("ReadApply mutated the baseline")
	}
	var deltaAfter protocol.ReadDelta
	if err := protocol.Decode([]byte(readWireText(t, delta)), &deltaAfter); err != nil {
		t.Fatalf("read delta after rejection: %v", err)
	}
	if !reflect.DeepEqual(deltaAfter, deltaBefore) {
		t.Fatalf("ReadApply mutated the delta")
	}
}

func mustReadRoundTrip(t *testing.T, delta *protocol.ReadDelta) *protocol.ReadDelta {
	t.Helper()
	encoded := readWireText(t, delta)
	var decoded protocol.ReadDelta
	if err := protocol.Decode([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decode read delta: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded read delta validation: %v", err)
	}
	return &decoded
}

func TestReadDeltaWrapperAddAndUnwrap(t *testing.T) {
	base := readRootR(readR("n15", protocol.RoleText, "survivor"))
	current := readRootR(readR("n90", protocol.RoleGroup, "wrapper",
		readR("n15", protocol.RoleText, "survivor"),
		readR("n91", protocol.RoleButton, "new"),
	))

	delta := mustReadDelta(t, "O1", base, current)
	if got := fmt.Sprint(readDefinitionRefs(delta.Add)); got != "[n90 n91]" {
		t.Fatalf("add roots = %s, want [n90 n91]", got)
	}
	for _, def := range delta.Add {
		if len(def.Children) != 0 {
			t.Fatalf("assembly definition %s must not inline children: %+v", def.Ref, def)
		}
	}
	requireReadChildBlock(t, delta, "n1", "n90")
	requireReadChildBlock(t, delta, "n90", "n15", "n91")
	mustReadApplyEqual(t, base, delta, current)
}

func TestReadDeltaChainBoundaryWithAssemblyRoot(t *testing.T) {
	leafNode := plainT("n4", protocol.RoleText)
	leafNode.Label = []string{"kept"}
	mid := plainT("n3", protocol.RoleView)
	mid.Children = []*protocol.Node{leafNode}
	head := plainT("n2", protocol.RoleView)
	head.Children = []*protocol.Node{mid}
	base := ProjectCompactTree(rootT(head))
	if !hasChain(base.Root, []string{"n2", "n3"}) {
		t.Fatalf("fixture did not produce a baseline chain: %+v", base.Root)
	}

	wrapper := &protocol.Node{
		Ref: "n90", Role: protocol.RoleGroup,
		Bounds:  &protocol.Bounds{Left: 0, Top: 0, Right: 200, Bottom: 200},
		Label:   []string{"wrapper"},
		Actions: []protocol.Action{},
		Enabled: readBool(true), Focused: readBool(false), Selected: readBool(false),
		Protected: readBool(false), Checked: readBool(false),
		Children: []*protocol.Node{head.Clone()},
	}
	current := ProjectCompactTree(rootT(wrapper))

	delta := mustReadDelta(t, "O1", base, current)
	if got := fmt.Sprint(readDefinitionRefs(delta.Add)); got != "[n90]" {
		t.Fatalf("chain boundary add roots = %s, want [n90]", got)
	}
	requireReadChildBlock(t, delta, "n1", "n90")
	requireReadChildBlock(t, delta, "n90", "n2")
	mustReadApplyEqual(t, base, delta, current)
}

func TestReadDeltaWireRoundTripAssembly(t *testing.T) {
	base := readRootR(readR("n15", protocol.RoleText, "survivor"))
	current := readRootR(readR("n90", protocol.RoleGroup, "wrapper",
		readR("n15", protocol.RoleText, "survivor"),
		readR("n91", protocol.RoleButton, "new"),
	))
	delta := mustReadDelta(t, "O1", base, current)
	decoded := mustReadRoundTrip(t, delta)
	if got := fmt.Sprint(readDefinitionRefs(decoded.Add)); got != "[n90 n91]" {
		t.Fatalf("decoded add roots = %s, want [n90 n91]", got)
	}
	requireReadChildBlock(t, decoded, "n90", "n15", "n91")
	mustReadApplyEqual(t, base, decoded, current)
}

func TestReadDeltaStandaloneAddRootChildrenOverride(t *testing.T) {
	base := readRootR(readR("n3", protocol.RoleText, "survivor"))
	current := readRootR(readR("n2", protocol.RoleGroup, "wrapper",
		readR("n3", protocol.RoleText, "survivor"),
		readR("n4", protocol.RoleButton, "new"),
	))
	delta := mustReadDelta(t, "O1", base, current)
	var standalone *protocol.ReadNode
	for _, def := range delta.Add {
		if def.Ref == "n2" {
			standalone = def
			break
		}
	}
	if standalone == nil {
		t.Fatalf("missing standalone assembly root n2: %+v", delta.Add)
	}
	if len(standalone.Children) != 0 {
		t.Fatalf("standalone assembly root must start with no inline children: %+v", standalone)
	}
	decoded := mustReadRoundTrip(t, delta)
	var decodedRoot *protocol.ReadNode
	for _, def := range decoded.Add {
		if def.Ref == "n2" {
			decodedRoot = def
			break
		}
	}
	if decodedRoot == nil || len(decodedRoot.Children) != 0 {
		t.Fatalf("strict wire round trip changed the assembly-root shape: %+v", decodedRoot)
	}
	requireReadChildBlock(t, decoded, "n2", "n3", "n4")
	mustReadApplyEqual(t, base, decoded, current)
}

func TestReadDeltaNestedClosedDefinitionRejectsChildren(t *testing.T) {
	base := readRootR()
	cases := []struct {
		name  string
		delta *protocol.ReadDelta
	}{
		{"nested_closed_node", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Add: []*protocol.ReadNode{
				readR("n2", protocol.RoleGroup, "add",
					readR("n3", protocol.RoleGroup, "closed")),
			},
			Children: map[string][]string{
				"n1": {"n2"},
				"n3": {},
			},
		}},
		{"top_level_add_with_inline_children", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Add: []*protocol.ReadNode{
				readR("n2", protocol.RoleGroup, "add",
					readR("n3", protocol.RoleText, "inline")),
			},
			Children: map[string][]string{
				"n1": {"n2"},
				"n2": {"n3"},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.delta.Validate(); err != nil {
				t.Fatalf("fixture validation: %v", err)
			}
			requireReadRejectedAtomically(t, base, tc.delta)
		})
	}
}

func movedReadReplaceTrees() (*protocol.ReadTree, *protocol.ReadTree) {
	var oldRows, newRows []*protocol.ReadNode
	for i := 10; i <= 17; i++ {
		oldRows = append(oldRows, readR(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("old-%d", i)))
		newRows = append(newRows, readR(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("new-%d", i)))
	}
	base := readRootR(
		readR("n2", protocol.RoleGroup, "a",
			readR("n4", protocol.RoleGroup, "r", oldRows...)),
		readR("n3", protocol.RoleGroup, "b"),
	)
	current := readRootR(
		readR("n2", protocol.RoleGroup, "a"),
		readR("n3", protocol.RoleGroup, "b",
			readR("n4", protocol.RoleGroup, "r", newRows...)),
	)
	return base, current
}

func TestReadDeltaHeaderAndStrictWireRoundTrip(t *testing.T) {
	base, current := movedReadReplaceTrees()
	delta := mustReadDelta(t, "O1", base, current)
	if _, ok := delta.Replace["n4"]; !ok {
		t.Fatalf("expected a moved replace region: %+v", delta.Replace)
	}
	requireReadChildBlock(t, delta, "n2")
	requireReadChildBlock(t, delta, "n3", "n4")
	encoded := readWireText(t, delta)
	if strings.Contains(encoded, `"defaults"`) || strings.Contains(encoded, `"position"`) {
		t.Fatalf("read delta carries a forbidden envelope: %s", encoded)
	}
	var wire map[string]any
	if err := protocol.Decode([]byte(encoded), &wire); err != nil {
		t.Fatalf("decode delta object: %v", err)
	}
	allowed := map[string]bool{
		"format": true, "base": true, "view": true,
		"set": true, "add": true, "remove": true, "children": true, "replace": true,
	}
	for key := range wire {
		if !allowed[key] {
			t.Fatalf("unexpected read delta member %q in %s", key, encoded)
		}
	}
	for _, key := range []string{"format", "base", "view"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("missing read delta header member %q", key)
		}
	}
	decoded := mustReadRoundTrip(t, delta)
	requireReadChildBlock(t, decoded, "n2")
	mustReadApplyEqual(t, base, decoded, current)
}

func TestReadDeltaFiveBoolStatesSurviveWire(t *testing.T) {
	baseNode := readR("n2", protocol.RoleText, "state")
	baseNode.Enabled = readBool(true)
	baseNode.Focused = readBool(false)
	baseNode.Selected = readBool(true)
	baseNode.Checked = readBool(false)
	baseNode.Protected = readBool(true)
	base := readRootR(baseNode)

	currentNode := readR("n2", protocol.RoleText, "state")
	currentNode.Enabled = nil
	currentNode.Focused = readBool(true)
	currentNode.Selected = nil
	currentNode.Checked = readBool(true)
	currentNode.Protected = readBool(false)
	current := readRootR(currentNode)

	delta := mustReadDelta(t, "O1", base, current)
	change, ok := delta.Set["n2"]
	if !ok {
		t.Fatalf("missing five-state set: %+v", delta)
	}
	for name, field := range map[string]protocol.Field[*bool]{
		"enabled": change.Enabled, "focused": change.Focused, "selected": change.Selected,
		"checked": change.Checked, "protected": change.Protected,
	} {
		if !field.Set {
			t.Fatalf("%s must be explicitly set", name)
		}
	}
	if change.Enabled.Value != nil || change.Focused.Value == nil || !*change.Focused.Value ||
		change.Selected.Value != nil || change.Checked.Value == nil || !*change.Checked.Value ||
		change.Protected.Value == nil || *change.Protected.Value {
		t.Fatalf("ReadDiff merged a five-state transition: %+v", change)
	}

	decoded := mustReadRoundTrip(t, delta)
	decodedChange := decoded.Set["n2"]
	if decodedChange.Enabled.Value != nil ||
		decodedChange.Focused.Value == nil || !*decodedChange.Focused.Value ||
		decodedChange.Selected.Value != nil ||
		decodedChange.Checked.Value == nil || !*decodedChange.Checked.Value ||
		decodedChange.Protected.Value == nil || *decodedChange.Protected.Value {
		t.Fatalf("wire round trip merged null, false and true: %+v", decodedChange)
	}
	applied, err := ReadApply(base, decoded)
	if err != nil {
		t.Fatalf("ReadApply: %v", err)
	}
	appliedNode := childOf(t, applied.Root, "n2")
	if appliedNode == nil ||
		appliedNode.Enabled != nil ||
		appliedNode.Focused == nil || !*appliedNode.Focused ||
		appliedNode.Selected != nil ||
		appliedNode.Checked == nil || !*appliedNode.Checked ||
		appliedNode.Protected == nil || *appliedNode.Protected {
		t.Fatalf("ReadApply lost a five-state value: %+v", appliedNode)
	}
	mustReadApplyEqual(t, base, decoded, current)
}

func TestMalformedReadDeltaRejectsClosedOverrideAndOrphan(t *testing.T) {
	base := readRootR(
		readR("n2", protocol.RoleGroup, "a",
			readR("n4", protocol.RoleGroup, "region"),
		),
		readR("n3", protocol.RoleGroup, "b"),
	)
	cases := []struct {
		name  string
		delta *protocol.ReadDelta
	}{
		{"closed_definition_override", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Add: []*protocol.ReadNode{
				readR("n20", protocol.RoleGroup, "new",
					readR("n21", protocol.RoleGroup, "closed")),
			},
			Children: map[string][]string{
				"n1":  {"n2", "n3", "n20"},
				"n21": {},
			},
		}},
		{"orphan_add", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Add: []*protocol.ReadNode{readR("n20", protocol.RoleText, "orphan")},
		}},
		{"replace_move_keeps_old_parent", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Replace: map[string]*protocol.ReadNode{
				"n4": readR("n4", protocol.RoleGroup, "region"),
			},
			Children: map[string][]string{"n3": {"n4"}},
		}},
		{"remove_root_revival", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Remove:   []string{"n2"},
			Children: map[string][]string{"n1": {"n2"}},
		}},
		{"dead_parent_children", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Remove:   []string{"n2"},
			Children: map[string][]string{"n2": {}},
		}},
		{"self_cycle", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Children: map[string][]string{"n2": {"n2"}},
		}},
		{"multiple_live_parents", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Children: map[string][]string{
				"n1": {"n2", "n3"},
				"n2": {"n3"},
			},
		}},
		{"unreachable_survivor", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Children: map[string][]string{"n1": {"n2"}},
		}},
		{"isolated_replace", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Replace: map[string]*protocol.ReadNode{
				"n4": readR("n4", protocol.RoleGroup, "region"),
			},
			Children: map[string][]string{
				"n1": {"n2"},
				"n2": {},
			},
		}},
		{"unreachable_explicit_cycle", &protocol.ReadDelta{
			Format: "delta", Base: "O1", View: protocol.ViewCompact,
			Add: []*protocol.ReadNode{readR("n20", protocol.RoleGroup, "cycle")},
			Children: map[string][]string{
				"n1":  {"n2"},
				"n3":  {"n20"},
				"n20": {"n3"},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.delta.Validate(); err != nil {
				t.Fatalf("fixture validation: %v", err)
			}
			requireReadRejectedAtomically(t, base, tc.delta)
		})
	}
}

func TestReadDeltaRemoveResidueUsesFinalReachability(t *testing.T) {
	base := readRootR(readR("n2", protocol.RoleGroup, "container",
		readR("n3", protocol.RoleText, "a"),
		readR("n4", protocol.RoleText, "b"),
	))

	pureRemove := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Remove: []string{"n2"},
	}
	got, err := ReadApply(base, pureRemove)
	if err != nil {
		t.Fatalf("pure remove: %v", err)
	}
	if len(got.Root.Children) != 0 {
		t.Fatalf("pure remove did not delete the residue")
	}

	current := readRootR(readR("n3", protocol.RoleText, "a",
		readR("n4", protocol.RoleText, "b"),
	))
	delta := mustReadDelta(t, "O1", base, current)
	if fmt.Sprint(delta.Remove) != "[n2]" {
		t.Fatalf("remove = %v, want [n2]", delta.Remove)
	}
	requireReadChildBlock(t, delta, "n1", "n3")
	requireReadChildBlock(t, delta, "n3", "n4")
	mustReadApplyEqual(t, base, delta, current)

	declared := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Remove: []string{"n2"},
		Set: map[string]protocol.ReadSetNode{
			"n3": {Label: protocol.Field[[]string]{Set: true, Value: []string{"declared"}}},
		},
	}
	requireReadRejectedAtomically(t, base, declared)
}

func TestReadDeltaHighestRemovedAncestorAcrossSurvivingMiddle(t *testing.T) {
	base := readRootR(readR("n2", protocol.RoleGroup, "outer",
		readR("n3", protocol.RoleGroup, "middle",
			readR("n4", protocol.RoleText, "gone")),
	))
	current := readRootR(readR("n3", protocol.RoleGroup, "middle"))
	delta := mustReadDelta(t, "O1", base, current)
	if fmt.Sprint(delta.Remove) != "[n2]" {
		t.Fatalf("remove = %v, want only n2", delta.Remove)
	}
	requireReadChildBlock(t, delta, "n1", "n3")
	requireReadChildBlock(t, delta, "n3")
	mustReadApplyEqual(t, base, delta, current)
}

func TestReadDeltaReplaceClosurePropagatesDeepParentChange(t *testing.T) {
	var bRows, cRows []*protocol.ReadNode
	for i := 10; i <= 15; i++ {
		bRows = append(bRows, readR(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("b-%d", i)))
		cRows = append(cRows, readR(fmt.Sprintf("n%d", i+10), protocol.RoleText, fmt.Sprintf("c-%d", i)))
	}
	base := readRootR(readR("n2", protocol.RoleGroup, "a",
		readR("n3", protocol.RoleGroup, "b", bRows...),
		readR("n4", protocol.RoleGroup, "c", cRows...),
	))
	current := readRootR(readR("n2", protocol.RoleGroup, "a",
		readR("n3", protocol.RoleGroup, "b",
			append(append([]*protocol.ReadNode{}, bRows...),
				readR("n4", protocol.RoleGroup, "c", cRows...))...),
	))
	delta := mustReadDelta(t, "O1", base, current)
	if len(delta.Replace) != 0 {
		t.Fatalf("deep parent change must not select a closed replace region: %+v", delta.Replace)
	}
	requireReadChildBlock(t, delta, "n2", "n3")
	requireReadChildBlock(t, delta, "n3", "n10", "n11", "n12", "n13", "n14", "n15", "n4")
	mustReadApplyEqual(t, base, delta, current)
}

func escapingReadReplaceTrees() (*protocol.ReadTree, *protocol.ReadTree) {
	var oldRows, newRows []*protocol.ReadNode
	for i := 10; i <= 17; i++ {
		oldRows = append(oldRows, readR(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("old-%d", i)))
		newRows = append(newRows, readR(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("new-%d", i)))
	}
	base := readRootR(readR("n2", protocol.RoleGroup, "outer",
		readR("n3", protocol.RoleGroup, "escape",
			readR("n4", protocol.RoleGroup, "region", oldRows...)),
	))
	current := readRootR(readR("n3", protocol.RoleGroup, "escape",
		readR("n4", protocol.RoleGroup, "region", newRows...),
	))
	return base, current
}

func TestReadDeltaReplaceSurvivesThroughEscapingAncestor(t *testing.T) {
	base, current := escapingReadReplaceTrees()
	delta := mustReadDelta(t, "O1", base, current)
	if fmt.Sprint(delta.Remove) != "[n2]" {
		t.Fatalf("remove = %v, want [n2]", delta.Remove)
	}
	if _, ok := delta.Replace["n4"]; !ok {
		if _, enclosing := delta.Replace["n3"]; !enclosing {
			t.Fatalf("replace inside the old removed closure is not represented: %+v", delta.Replace)
		}
	}
	requireReadChildBlock(t, delta, "n1", "n3")
	mustReadApplyEqual(t, base, delta, current)
}
