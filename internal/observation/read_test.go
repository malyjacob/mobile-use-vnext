package observation

import (
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
	if !strings.Contains(text, `"defaults":{"checked":false}`) {
		t.Fatalf("compact tree must declare the checked default: %s", text)
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

func TestGeometryStatesAreDistinct(t *testing.T) {
	unknown := plainT("n2", protocol.RoleView)
	unknown.Bounds = nil
	anonymous := plainT("n3", protocol.RoleGroup)
	functional := plainT("n4", protocol.RoleButton)
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
		"root role":       func(n *protocol.Node) { n.Role = protocol.RoleRoot },
		"context":         func(n *protocol.Node) { n.Context = true },
		"action":          func(n *protocol.Node) { n.Actions = []protocol.Action{protocol.ActionTap} },
		"label":           func(n *protocol.Node) { n.Label = []string{"x"} },
		"empty value":     func(n *protocol.Node) { n.Value = readString("") },
		"empty hint":      func(n *protocol.Node) { n.Hint = readString("") },
		"empty resource":  func(n *protocol.Node) { n.Resource = readString("") },
		"axis":            func(n *protocol.Node) { n.Axis = protocol.AxisY },
		"unknown enabled": func(n *protocol.Node) { n.Enabled = nil },
		"disabled":        func(n *protocol.Node) { n.Enabled = readBool(false) },
		"unknown focused": func(n *protocol.Node) { n.Focused = nil },
		"unknown checked": func(n *protocol.Node) { n.Checked = nil },
		"selected":        func(n *protocol.Node) { n.Selected = readBool(true) },
		"protected":       func(n *protocol.Node) { n.Protected = readBool(true) },
		"input role":      func(n *protocol.Node) { n.Role = protocol.RoleInput },
		"scroll role":     func(n *protocol.Node) { n.Role = protocol.RoleScroll },
		"image role":      func(n *protocol.Node) { n.Role = protocol.RoleImage },
		"text role":       func(n *protocol.Node) { n.Role = protocol.RoleText },
		"checkable role":  func(n *protocol.Node) { n.Role = protocol.RoleCheckbox },
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
