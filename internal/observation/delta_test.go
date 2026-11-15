package observation

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// leaf builds a public node with a single label and default state.
func leaf(ref string, role protocol.Role, label string) *protocol.Node {
	return &protocol.Node{Ref: ref, Role: role, Label: []string{label}}
}

// group builds a public node with children.
func group(ref string, role protocol.Role, children ...*protocol.Node) *protocol.Node {
	return &protocol.Node{Ref: ref, Role: role, Children: children}
}

func fullTree(root *protocol.Node) *protocol.Tree {
	return &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
}

// twoCardTree reproduces the design example shape with fixed refs.
func twoCardTree() *protocol.Tree {
	return fullTree(group("n1", protocol.RoleRoot,
		&protocol.Node{Ref: "n2", Role: protocol.RoleCheckbox, Label: []string{"仅看未读"}, Actions: []protocol.Action{protocol.ActionTap}, Checked: boolp(false)},
		leaf("n3", protocol.RoleInput, "搜索"),
		group("n4", protocol.RoleScroll,
			group("n5", protocol.RoleGroup,
				leaf("n6", protocol.RoleText, "帖子甲"),
				leaf("n7", protocol.RoleButton, "收藏"),
			),
			group("n8", protocol.RoleGroup,
				leaf("n9", protocol.RoleText, "帖子乙"),
				leaf("n10", protocol.RoleButton, "收藏"),
			),
		),
	))
}

func boolp(v bool) *bool    { return &v }
func strp(v string) *string { return &v }

func TestDiffEmpty(t *testing.T) {
	base := twoCardTree()
	current := twoCardTree()
	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if delta.Format != "delta" || delta.Base != "O100" || !delta.IsEmpty() {
		t.Fatalf("delta = %+v", delta)
	}
	applied, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(applied, current) {
		t.Fatalf("apply(empty) != current")
	}
}

func TestDiffSetExplicitValues(t *testing.T) {
	base := twoCardTree()
	current := twoCardTree()

	// checked true (n2), then back to explicit false and cleared label/actions.
	n2 := find(current, "n2")
	n2.Checked = boolp(true)
	n2.Label = []string{}
	n2.Actions = []protocol.Action{}
	n2.Enabled = boolp(false)
	// input value to a known empty string (explicit "").
	n3 := find(current, "n3")
	n3.Role = protocol.RoleInput
	n3.Value = strp("")
	n3.Focused = boolp(true)

	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	change, ok := delta.Set["n2"]
	if !ok {
		t.Fatalf("missing set for n2: %+v", delta)
	}
	if !change.Label.Set || len(change.Label.Value) != 0 {
		t.Fatalf("label must be an explicit empty array, got %+v", change.Label)
	}
	if !change.Actions.Set || len(change.Actions.Value) != 0 {
		t.Fatalf("actions must be an explicit empty array, got %+v", change.Actions)
	}
	if !change.Checked.Set || change.Checked.Value == nil || !*change.Checked.Value {
		t.Fatalf("checked must be explicit true, got %+v", change.Checked)
	}
	if !change.Enabled.Set || change.Enabled.Value == nil || *change.Enabled.Value {
		t.Fatalf("enabled must be explicit false, got %+v", change.Enabled)
	}
	v, ok := delta.Set["n3"]
	if !ok || !v.Value.Set || v.Value.Value == nil || *v.Value.Value != "" {
		t.Fatalf("value must be explicit empty string, got %+v", delta.Set["n3"])
	}
	if !v.Focused.Set || v.Focused.Value == nil || !*v.Focused.Value {
		t.Fatalf("focused must be explicit true, got %+v", v.Focused)
	}
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("apply != current:\n%s\n%s", dump(got), dump(current))
	}
}

func TestDiffSetToNull(t *testing.T) {
	base := twoCardTree()
	n2 := find(base, "n2")
	n2.Enabled = boolp(false)
	n2.Checked = boolp(true)

	current := twoCardTree()
	c2 := find(current, "n2")
	c2.Enabled = nil
	c2.Checked = nil

	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	change := delta.Set["n2"]
	if !change.Enabled.Set || change.Enabled.Value != nil {
		t.Fatalf("enabled must be explicit null, got %+v", change.Enabled)
	}
	if !change.Checked.Set || change.Checked.Value != nil {
		t.Fatalf("checked must be explicit null, got %+v", change.Checked)
	}
	// Axis changed from y (n4) to empty: explicit null axis.
	find(current, "n4").Axis = ""
	find(base, "n4").Axis = protocol.AxisY
	delta, err = Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	axis := delta.Set["n4"].Axis
	if !axis.Set || axis.Value != nil {
		t.Fatalf("axis must be explicit null, got %+v", axis)
	}
}

func TestDiffRemoveAndChildren(t *testing.T) {
	base := twoCardTree()
	current := twoCardTree()
	// Remove the second card subtree (n8, n9, n10) but keep n4.
	scroll := find(current, "n4")
	scroll.Children = scroll.Children[:1]

	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(delta.Replace) == 0 {
		if len(delta.Remove) != 1 || delta.Remove[0] != "n8" {
			t.Fatalf("remove = %v, want the highest removed root n8 or one enclosing replace (delta %+v)",
				delta.Remove, delta)
		}
		if len(delta.Children) != 0 {
			t.Fatalf("a pure remove must not need a children block: %v", delta.Children)
		}
	}
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("apply != current")
	}
}

func TestDiffAddSubtree(t *testing.T) {
	base := twoCardTree()
	current := twoCardTree()
	// A new card appears at the end of the scroll area.
	card := group("n20", protocol.RoleGroup,
		leaf("n21", protocol.RoleText, "帖子丙"),
		leaf("n22", protocol.RoleButton, "收藏"),
	)
	find(current, "n4").Children = append(find(current, "n4").Children, card)

	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(delta.Add) != 1 || delta.Add[0].Ref != "n20" || len(delta.Add[0].Children) != 2 {
		t.Fatalf("add = %+v", delta.Add)
	}
	if got := delta.Children["n4"]; fmt.Sprint(got) != "[n5 n8 n20]" {
		t.Fatalf("children[n4] = %v", got)
	}
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("apply != current")
	}
}

func TestDiffReorderChildren(t *testing.T) {
	base := twoCardTree()
	current := twoCardTree()
	scroll := find(current, "n4")
	scroll.Children = []*protocol.Node{scroll.Children[1], scroll.Children[0]}

	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if got := fmt.Sprint(delta.Children["n4"]); got != "[n8 n5]" {
		t.Fatalf("children[n4] = %v", got)
	}
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("apply != current")
	}
}

func TestDiffReplaceRegion(t *testing.T) {
	base := twoCardTree()
	current := twoCardTree()
	// The first card changes pervasively: one child removed, one added, and a
	// label change. Ref n5 survives, n6 changes, n7 disappears, n30 appears.
	card := find(current, "n5")
	card.Children = []*protocol.Node{leaf("n30", protocol.RoleText, "帖子丁"), find(current, "n7")}
	find(current, "n7").Label = []string{"已收藏"}
	find(current, "n7").Enabled = boolp(false)

	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(delta.Replace) != 0 {
		// The explicit form is also legal for a region of this size; both are
		// accepted. Whatever Diff chose must still apply correctly.
	}
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("apply != current\n%s\n%s", dump(got), dump(current))
	}
}

func TestDiffReplaceChosenForLargeRegion(t *testing.T) {
	// A card with many nodes is cheaper to replace than to patch node by node.
	build := func(prefix string, labelSuffix string) *protocol.Tree {
		var children []*protocol.Node
		for i := 0; i < 8; i++ {
			children = append(children, leaf(fmt.Sprintf("%s%d", prefix, i), protocol.RoleText, fmt.Sprintf("行%d%s", i, labelSuffix)))
		}
		return fullTree(group("n1", protocol.RoleRoot,
			group("n2", protocol.RoleScroll, group("n3", protocol.RoleGroup, children...)),
		))
	}
	base := build("p", "")
	current := build("p", "改")
	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(delta.Replace) != 1 {
		t.Fatalf("expected one replace region, got %+v (set=%d children=%d)", delta.Replace, len(delta.Set), len(delta.Children))
	}
	// The scroll container n2 is the region: it covers n3 and all rows in one
	// definition, which is cheaper than replacing n3 alone.
	if _, ok := delta.Replace["n2"]; !ok {
		t.Fatalf("replace region = %v, want n2", keys(delta.Replace))
	}
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("apply != current")
	}
}

func keys(m map[string]*protocol.Node) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestDiffRejectsRoleChange(t *testing.T) {
	base := twoCardTree()
	current := twoCardTree()
	find(current, "n6").Role = protocol.RoleButton
	if _, err := Diff("O100", base, current); !errors.Is(err, ErrNotRepresentable) {
		t.Fatalf("role change: err = %v, want ErrNotRepresentable", err)
	}
}

func TestDiffCrossParentMoveUsesChildren(t *testing.T) {
	// The design allows a cross-parent move when the ref continues: both
	// parents publish their complete current child list and no move primitive
	// is needed. The result must still apply exactly.
	base := twoCardTree()
	current := twoCardTree()
	first := find(current, "n5")
	moved := first.Children[1]
	first.Children = first.Children[:1]
	second := find(current, "n8")
	second.Children = append(second.Children, moved)

	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if _, ok := delta.Children["n5"]; !ok {
		t.Fatalf("source parent must publish its new list: %v", delta.Children)
	}
	if _, ok := delta.Children["n8"]; !ok {
		t.Fatalf("target parent must publish its new list: %v", delta.Children)
	}
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("apply != current")
	}
}

func TestDiffRejectsMoveIntoOtherParentUnderRemovedRegion(t *testing.T) {
	// Removing a parent while one of its children survives elsewhere needs a
	// region replace; if no reliable common ancestor can cover both sides the
	// pair is rejected. Here the common ancestor is n4, which is a legal
	// region, so Diff must succeed with a replace instead of a bare remove.
	base := twoCardTree()
	current := twoCardTree()
	first := find(current, "n5")
	moved := first.Children[1]
	first.Children = nil
	second := find(current, "n8")
	second.Children = append(second.Children, moved)

	delta, err := Diff("O100", base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("apply != current")
	}
}

func TestDiffRejectsRootChange(t *testing.T) {
	base := twoCardTree()
	current := twoCardTree()
	current.Root.Ref = "n9"
	if _, err := Diff("O100", base, current); !errors.Is(err, ErrNotRepresentable) {
		t.Fatalf("root ref change: err = %v", err)
	}
}

func TestApplyRejectsIllegalDeltas(t *testing.T) {
	base := twoCardTree()
	cases := []struct {
		name  string
		delta *protocol.Delta
	}{
		{"wrong format", &protocol.Delta{Format: "full", Base: "O1"}},
		{"missing base", &protocol.Delta{Format: "delta"}},
		{"set unknown ref", &protocol.Delta{Format: "delta", Base: "O1",
			Set: map[string]protocol.SetNode{"n99": {Label: protocol.Field[[]string]{Set: true, Value: []string{"x"}}}}}},
		{"set root interaction field", &protocol.Delta{Format: "delta", Base: "O1",
			Set: map[string]protocol.SetNode{"n1": {Checked: protocol.Field[*bool]{Set: true, Value: boolp(true)}}}}},
		{"remove root", &protocol.Delta{Format: "delta", Base: "O1", Remove: []string{"n1"}}},
		{"remove unknown", &protocol.Delta{Format: "delta", Base: "O1", Remove: []string{"n99"}}},
		{"nested remove", &protocol.Delta{Format: "delta", Base: "O1", Remove: []string{"n5", "n6"}}},
		{"add existing ref", &protocol.Delta{Format: "delta", Base: "O1", Add: []*protocol.Node{leaf("n6", protocol.RoleText, "x")}}},
		{"duplicate add", &protocol.Delta{Format: "delta", Base: "O1",
			Add: []*protocol.Node{leaf("n40", protocol.RoleText, "a"), leaf("n40", protocol.RoleText, "b")}}},
		{"children unknown ref", &protocol.Delta{Format: "delta", Base: "O1",
			Children: map[string][]string{"n4": {"n99"}}}},
		{"orphan add", &protocol.Delta{Format: "delta", Base: "O1",
			Add: []*protocol.Node{leaf("n40", protocol.RoleText, "a")}}},
		{"set inside add", &protocol.Delta{Format: "delta", Base: "O1",
			Add:      []*protocol.Node{leaf("n40", protocol.RoleText, "a")},
			Children: map[string][]string{"n4": {"n5", "n8", "n40"}},
			Set:      map[string]protocol.SetNode{"n40": {Label: protocol.Field[[]string]{Set: true, Value: []string{"b"}}}}}},
		{"replace role change", &protocol.Delta{Format: "delta", Base: "O1",
			Replace: map[string]*protocol.Node{"n5": group("n5", protocol.RoleButton, leaf("n6", protocol.RoleText, "x"))}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := Apply(base, tc.delta)
			if err == nil {
				t.Fatalf("Apply accepted an illegal delta, got %s", dump(tree))
			}
			if !errors.Is(err, ErrNotRepresentable) {
				t.Fatalf("error = %v, want ErrNotRepresentable", err)
			}
		})
	}
}

func TestApplyAcceptsReplace(t *testing.T) {
	base := twoCardTree()
	delta := &protocol.Delta{Format: "delta", Base: "O1",
		Replace: map[string]*protocol.Node{
			"n5": group("n5", protocol.RoleGroup,
				leaf("n31", protocol.RoleText, "帖子丁"),
				leaf("n32", protocol.RoleButton, "收藏"),
			),
		}}
	tree, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !equalNode(tree.Root.Children[2].Children[0], delta.Replace["n5"]) {
		t.Fatalf("replace region not installed: %s", dump(tree))
	}
	if len(tree.Root.Children[2].Children) != 2 {
		t.Fatalf("replace must keep sibling order and count")
	}
}

func TestApplyDoesNotMutateBase(t *testing.T) {
	base := twoCardTree()
	before := dump(base)
	delta := &protocol.Delta{Format: "delta", Base: "O1",
		Set: map[string]protocol.SetNode{"n6": {Label: protocol.Field[[]string]{Set: true, Value: []string{"新"}}}}}
	if _, err := Apply(base, delta); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if dump(base) != before {
		t.Fatalf("Apply mutated the baseline")
	}
}

// randomPair builds a baseline and a current tree that share one skeleton
// (so no node moves parent) while labels, bounds and states mutate, children
// are removed, added at the end, and reordered. This exercises every legal
// delta form; parent moves and role changes are covered by the rejection tests.
func randomPair(seed int64) (*protocol.Tree, *protocol.Tree) {
	r := rand.New(rand.NewSource(seed))
	newState := func(*protocol.Node) *protocol.Node {
		n := &protocol.Node{
			Ref:  "x",
			Role: protocol.RoleText,
		}
		switch r.Intn(4) {
		case 0:
			n.Label = []string{fmt.Sprintf("标签%d", r.Intn(4))}
		case 1:
			n.Label = []string{}
		}
		if r.Intn(3) == 0 {
			b := protocol.Bounds{Left: r.Intn(10), Top: r.Intn(10), Right: 100, Bottom: 100}
			n.Bounds = &b
		}
		if r.Intn(3) == 0 {
			n.Enabled = boolp(r.Intn(2) == 0)
		}
		if r.Intn(4) == 0 {
			n.Checked = boolp(r.Intn(2) == 0)
		}
		if r.Intn(5) == 0 {
			n.Actions = []protocol.Action{protocol.ActionTap}
		}
		return n
	}

	var buildBase func(ref string, depth int) *protocol.Node
	buildBase = func(ref string, depth int) *protocol.Node {
		n := newState(nil)
		n.Ref = ref
		if depth >= 3 {
			return n
		}
		for i := 0; i < 1+r.Intn(3); i++ {
			n.Children = append(n.Children, buildBase(fmt.Sprintf("%s_%d", ref, i), depth+1))
		}
		return n
	}
	baseRoot := &protocol.Node{Ref: "root", Role: protocol.RoleRoot}
	for i := 0; i < 2+r.Intn(2); i++ {
		baseRoot.Children = append(baseRoot.Children, buildBase(fmt.Sprintf("b%d", i), 0))
	}

	// Current: same skeleton with mutated fields.
	var mutate func(src *protocol.Node) *protocol.Node
	mutate = func(src *protocol.Node) *protocol.Node {
		n := newState(nil)
		n.Ref = src.Ref
		n.Role = src.Role
		if r.Intn(4) == 0 {
			n.Label = src.Label
		}
		for _, child := range src.Children {
			if r.Intn(8) == 0 {
				continue // removed child (its whole subtree leaves)
			}
			n.Children = append(n.Children, mutate(child))
		}
		if r.Intn(3) == 0 && len(n.Children) > 1 {
			// Reorder siblings.
			i, j := r.Intn(len(n.Children)), r.Intn(len(n.Children))
			n.Children[i], n.Children[j] = n.Children[j], n.Children[i]
		}
		return n
	}
	curRoot := &protocol.Node{Ref: baseRoot.Ref, Role: protocol.RoleRoot}
	added := 0
	for _, child := range baseRoot.Children {
		if r.Intn(6) == 0 {
			continue
		}
		node := mutate(child)
		if r.Intn(5) == 0 {
			// A new sibling subtree appears at the end.
			added++
			sub := &protocol.Node{Ref: fmt.Sprintf("new%d", added), Role: protocol.RoleGroup,
				Children: []*protocol.Node{{Ref: fmt.Sprintf("new%d_0", added), Role: protocol.RoleText, Label: []string{"新增"}}}}
			node.Children = append(node.Children, sub)
		}
		curRoot.Children = append(curRoot.Children, node)
	}
	return fullTree(baseRoot), fullTree(curRoot)
}

func TestDiffApplyRoundTripRandom(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		base, current := randomPair(seed)

		delta, err := Diff("O42", base, current)
		if err != nil {
			t.Fatalf("seed %d: Diff: %v", seed, err)
		}
		got, err := Apply(base, delta)
		if err != nil {
			t.Fatalf("seed %d: Apply: %v (delta %+v)", seed, err, delta)
		}
		if !Equal(got, current) {
			t.Fatalf("seed %d: apply != current\nbase:\n%s\ncurrent:\n%s\ndelta set=%d add=%d remove=%v children=%v replace=%v",
				seed, dump(base), dump(current), len(delta.Set), len(delta.Add), delta.Remove, delta.Children, delta.Replace)
		}
		if delta.Format != "delta" || delta.Base != "O42" {
			t.Fatalf("seed %d: header = %q/%q", seed, delta.Format, delta.Base)
		}
	}
}

func TestEqualCanonicalDefaults(t *testing.T) {
	a := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{
		Ref: "n2", Role: protocol.RoleText,
		Enabled: boolp(true), Focused: boolp(false), Selected: boolp(false), Protected: boolp(false), Checked: boolp(false),
	}))
	b := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{
		Ref: "n2", Role: protocol.RoleText, Label: []string{}, Actions: []protocol.Action{},
		Enabled: boolp(true), Focused: boolp(false), Selected: boolp(false), Protected: boolp(false), Checked: boolp(false),
	}))
	if !Equal(a, b) {
		t.Fatalf("empty collections and identical tri-state values must compare equal")
	}
	// Internal nil (unknown) must never equal a known default.
	c := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{Ref: "n2", Role: protocol.RoleText}))
	if Equal(b, c) {
		t.Fatalf("internal nil flags must not equal known defaults")
	}
	// Wire decoding of absent tri-state members restores known defaults.
	const wireAbsent = `{"format":"full","scope":"reported_visible_with_context","root":{"ref":"n1","role":"root","children":[{"ref":"n2","role":"text"}]}}`
	var decoded protocol.Tree
	if err := protocol.Decode([]byte(wireAbsent), &decoded); err != nil {
		t.Fatalf("protocol.Decode(wireAbsent): %v", err)
	}
	rootWithDefaults := fullTree(&protocol.Node{
		Ref: "n1", Role: protocol.RoleRoot,
		Enabled: boolp(true), Focused: boolp(false), Selected: boolp(false), Protected: boolp(false), Checked: boolp(false),
		Children: b.Root.Children,
	})
	if !Equal(&decoded, rootWithDefaults) {
		t.Fatalf("absent wire tri-state members must decode to known defaults")
	}
	d := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{
		Ref: "n2", Role: protocol.RoleText,
		Enabled: boolp(false), Focused: boolp(false), Selected: boolp(false), Protected: boolp(false), Checked: boolp(false),
	}))
	if Equal(a, d) {
		t.Fatalf("known false must differ from true")
	}
	e := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{
		Ref: "n2", Role: protocol.RoleText, Context: true,
		Enabled: boolp(true), Focused: boolp(false), Selected: boolp(false), Protected: boolp(false), Checked: boolp(false),
	}))
	if Equal(a, e) {
		t.Fatalf("context must take part in equality")
	}
}

// TestBuildDiffApplyEndToEnd proves the package's core promise with the real
// pipeline: two consecutive captures of one session, a builder for refs, and a
// delta between their snapshots that must reproduce the current tree.
func TestBuildDiffApplyEndToEnd(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, twoCards().capture())

	second := twoCards()
	// Type into the input: a value change that must keep the input's ref.
	second.nodes[1].Text, second.nodes[1].TextSet = "测试", true

	snap := buildOne(t, b, second.capture())
	base := &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: first.Tree.Root.Clone()}

	delta, err := Diff("O100", base, snap.Tree)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, snap.Tree) {
		t.Fatalf("apply(base, delta) != current\nbase:\n%s\ncurrent:\n%s\napplied:\n%s",
			dump(base), dump(snap.Tree), dump(got))
	}
	if _, ok := delta.Set[first.Tree.Root.Children[0].Ref]; !ok {
		t.Fatalf("the input value change must be expressed as a set on its stable ref: %+v", delta)
	}
}

// TestBuildDeltaAcrossSessionsKeepsRefsAndDeltaIsEmpty checks that a full
// rebuild of identical content produces no delta at all.
func TestBuildDeltaAcrossSessionsKeepsRefsAndDeltaIsEmpty(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, twoCards().capture())
	second := buildOne(t, b, twoCards().capture())
	base := &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: first.Tree.Root.Clone()}
	delta, err := Diff("O100", base, second.Tree)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !delta.IsEmpty() {
		t.Fatalf("identical content must produce an empty delta, got %+v", delta)
	}
}

func mustDelta(t *testing.T, baseID string, base, current *protocol.Tree) *protocol.Delta {
	t.Helper()
	delta, err := Diff(baseID, base, current)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	return delta
}

func mustApplyEqual(t *testing.T, base *protocol.Tree, delta *protocol.Delta, current *protocol.Tree) {
	t.Helper()
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("apply != current\nbase:\n%s\ncurrent:\n%s\napplied:\n%s\ndelta: %+v",
			dump(base), dump(current), dump(got), delta)
	}
}

func requireChildBlock(t *testing.T, delta *protocol.Delta, parent string, want ...string) {
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

func definitionRefs(defs []*protocol.Node) []string {
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		if def != nil {
			out = append(out, def.Ref)
		}
	}
	return out
}

func jsonText(t *testing.T, value any) string {
	t.Helper()
	encoded, err := protocol.JSON(value)
	if err != nil {
		t.Fatalf("protocol.JSON: %v", err)
	}
	return string(encoded)
}

func requireRejectedAtomically(t *testing.T, base *protocol.Tree, delta *protocol.Delta) {
	t.Helper()
	baseBefore := &protocol.Tree{
		Format: base.Format, Scope: base.Scope, Root: base.Root.Clone(),
	}
	var deltaBefore protocol.Delta
	if err := protocol.Decode([]byte(jsonText(t, delta)), &deltaBefore); err != nil {
		t.Fatalf("clone delta: %v", err)
	}
	got, err := Apply(base, delta)
	if err == nil {
		t.Fatalf("Apply accepted an illegal delta: %s", dump(got))
	}
	if !errors.Is(err, ErrNotRepresentable) {
		t.Fatalf("Apply error = %v, want ErrNotRepresentable", err)
	}
	if got != nil {
		t.Fatalf("Apply returned a partial tree on rejection")
	}
	if !Equal(base, baseBefore) {
		t.Fatalf("Apply mutated the baseline")
	}
	var deltaAfter protocol.Delta
	if err := protocol.Decode([]byte(jsonText(t, delta)), &deltaAfter); err != nil {
		t.Fatalf("read delta after rejection: %v", err)
	}
	if !reflect.DeepEqual(deltaAfter, deltaBefore) {
		t.Fatalf("Apply mutated the delta")
	}
}

func TestDeltaSurvivingNodeMovesIntoNewParentContainer(t *testing.T) {
	base := fullTree(group("root", protocol.RoleRoot,
		leaf("n15", protocol.RoleText, "survivor"),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		group("n90", protocol.RoleGroup,
			leaf("n15", protocol.RoleText, "survivor"),
			leaf("n91", protocol.RoleButton, "new"),
		),
	))

	delta := mustDelta(t, "O1", base, current)
	if got := fmt.Sprint(definitionRefs(delta.Add)); got != "[n90 n91]" {
		t.Fatalf("add roots = %s, want [n90 n91]", got)
	}
	for _, def := range delta.Add {
		if len(def.Children) != 0 {
			t.Fatalf("assembly add %s must carry only its own fields: %+v", def.Ref, def)
		}
	}
	if len(delta.Remove) != 0 || len(delta.Replace) != 0 {
		t.Fatalf("wrapper move must not remove or replace: %+v", delta)
	}
	requireChildBlock(t, delta, "root", "n90")
	requireChildBlock(t, delta, "n90", "n15", "n91")
	mustApplyEqual(t, base, delta, current)
}

func TestDeltaDeepAssemblyFrontier(t *testing.T) {
	base := fullTree(group("root", protocol.RoleRoot,
		leaf("n15", protocol.RoleText, "leaf"),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		group("n90", protocol.RoleGroup,
			group("n91", protocol.RoleGroup,
				leaf("n15", protocol.RoleText, "leaf"),
				leaf("n92", protocol.RoleButton, "deep"),
			),
		),
	))

	delta := mustDelta(t, "O1", base, current)
	if got := fmt.Sprint(definitionRefs(delta.Add)); got != "[n90 n91 n92]" {
		t.Fatalf("add roots = %s, want every assembly frontier root", got)
	}
	for _, def := range delta.Add {
		if len(def.Children) != 0 {
			t.Fatalf("frontier root %s must not inline children: %+v", def.Ref, def)
		}
	}
	requireChildBlock(t, delta, "root", "n90")
	requireChildBlock(t, delta, "n90", "n91")
	requireChildBlock(t, delta, "n91", "n15", "n92")
	mustApplyEqual(t, base, delta, current)
}

func TestDeltaOldContainerRemovedWhileDescendantSurvives(t *testing.T) {
	base := fullTree(group("root", protocol.RoleRoot,
		group("n10", protocol.RoleGroup,
			leaf("n15", protocol.RoleText, "kept"),
			leaf("n16", protocol.RoleText, "gone"),
		),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		leaf("n15", protocol.RoleText, "kept"),
	))

	delta := mustDelta(t, "O1", base, current)
	if fmt.Sprint(delta.Remove) != "[n10]" {
		t.Fatalf("remove = %v, want only n10", delta.Remove)
	}
	requireChildBlock(t, delta, "root", "n15")
	mustApplyEqual(t, base, delta, current)
}

func TestDeltaExistingParentCrossMoveStillUsesChildren(t *testing.T) {
	base := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup, leaf("n3", protocol.RoleText, "moved")),
		group("n4", protocol.RoleGroup),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup),
		group("n4", protocol.RoleGroup, leaf("n3", protocol.RoleText, "moved")),
	))

	delta := mustDelta(t, "O1", base, current)
	requireChildBlock(t, delta, "n2")
	requireChildBlock(t, delta, "n4", "n3")
	if fmt.Sprint(delta.Remove) != "[]" {
		t.Fatalf("cross-parent move is not a removal: %v", delta.Remove)
	}
	mustApplyEqual(t, base, delta, current)
}

func movedReplaceTrees() (*protocol.Tree, *protocol.Tree) {
	var oldRows, newRows []*protocol.Node
	for i := 10; i <= 17; i++ {
		oldRows = append(oldRows, leaf(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("old-%d", i)))
		newRows = append(newRows, leaf(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("new-%d", i)))
	}
	base := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup,
			group("n4", protocol.RoleGroup, oldRows...),
		),
		group("n3", protocol.RoleGroup),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup),
		group("n3", protocol.RoleGroup,
			group("n4", protocol.RoleGroup, newRows...),
		),
	))
	return base, current
}

func TestDeltaMovedReplaceRegionDoesNotUseOldParentSlot(t *testing.T) {
	base, current := movedReplaceTrees()
	delta := mustDelta(t, "O1", base, current)
	if _, ok := delta.Replace["n4"]; !ok {
		t.Fatalf("expected a replace definition for the moved region: %+v", delta.Replace)
	}
	requireChildBlock(t, delta, "n2")
	requireChildBlock(t, delta, "n3", "n4")
	mustApplyEqual(t, base, delta, current)
}

func TestDeltaRemoveResidueUsesFinalReachability(t *testing.T) {
	t.Run("remove_root_alone_deletes_its_old_subtree", func(t *testing.T) {
		base := fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup,
				leaf("n3", protocol.RoleText, "a"),
				leaf("n4", protocol.RoleText, "b"),
			),
		))
		delta := &protocol.Delta{Format: "delta", Base: "O1", Remove: []string{"n2"}}
		got, err := Apply(base, delta)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(got.Root.Children) != 0 {
			t.Fatalf("remove residue was not deleted: %s", dump(got))
		}
	})

	t.Run("surviving_children_escape_the_removed_container", func(t *testing.T) {
		base := fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup,
				leaf("n3", protocol.RoleText, "a"),
				leaf("n4", protocol.RoleText, "b"),
			),
		))
		current := fullTree(group("root", protocol.RoleRoot,
			group("n3", protocol.RoleText,
				leaf("n4", protocol.RoleText, "b"),
			),
		))
		delta := mustDelta(t, "O1", base, current)
		if fmt.Sprint(delta.Remove) != "[n2]" {
			t.Fatalf("remove = %v, want highest removed root n2", delta.Remove)
		}
		requireChildBlock(t, delta, "root", "n3")
		requireChildBlock(t, delta, "n3", "n4")
		mustApplyEqual(t, base, delta, current)
	})

	t.Run("declared_survivor_must_be_reachable", func(t *testing.T) {
		base := fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup,
				leaf("n3", protocol.RoleText, "a"),
				leaf("n4", protocol.RoleText, "b"),
			),
		))
		delta := &protocol.Delta{
			Format: "delta", Base: "O1", Remove: []string{"n2"},
			Set: map[string]protocol.SetNode{
				"n3": {Label: protocol.Field[[]string]{Set: true, Value: []string{"declared"}}},
			},
		}
		requireRejectedAtomically(t, base, delta)
	})
}

func TestDeltaApplyRejectsCycleMultiParentAndOrphan(t *testing.T) {
	base := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup),
		group("n3", protocol.RoleGroup),
		group("n4", protocol.RoleGroup),
	))
	cases := []struct {
		name  string
		delta *protocol.Delta
	}{
		{"self_cycle", &protocol.Delta{Format: "delta", Base: "O1",
			Children: map[string][]string{"n2": {"n2"}}}},
		{"cross_subtree_cycle", &protocol.Delta{Format: "delta", Base: "O1",
			Children: map[string][]string{
				"root": {"n2"},
				"n2":   {"n3"},
				"n3":   {"n2"},
			}}},
		{"multiple_live_parents", &protocol.Delta{Format: "delta", Base: "O1",
			Children: map[string][]string{
				"root": {"n2", "n3"},
				"n2":   {"n3"},
			}}},
		{"unattached_add", &protocol.Delta{Format: "delta", Base: "O1",
			Add: []*protocol.Node{leaf("n50", protocol.RoleText, "orphan")}}},
		{"unreachable_survivor", &protocol.Delta{Format: "delta", Base: "O1",
			Children: map[string][]string{"root": {"n2"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireRejectedAtomically(t, base, tc.delta)
		})
	}
}

func TestDeltaChildrenEmptyListIsExplicit(t *testing.T) {
	base := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup, leaf("n3", protocol.RoleText, "moved")),
		group("n4", protocol.RoleGroup),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup),
		group("n4", protocol.RoleGroup, leaf("n3", protocol.RoleText, "moved")),
	))

	delta := mustDelta(t, "O1", base, current)
	requireChildBlock(t, delta, "n2")
	encoded := jsonText(t, delta)
	if !strings.Contains(encoded, `"n2":[]`) {
		t.Fatalf("explicit empty children list was encoded as absence: %s", encoded)
	}
	var decoded protocol.Delta
	if err := protocol.Decode([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	if list, ok := decoded.Children["n2"]; !ok || list == nil || len(list) != 0 {
		t.Fatalf("empty children list was lost in strict round trip: %+v", decoded.Children)
	}
	mustApplyEqual(t, base, delta, current)
}

func replaceInteriorTrees(includeNew bool) (*protocol.Tree, *protocol.Tree) {
	var baseRows, currentRows []*protocol.Node
	for i := 10; i <= 18; i++ {
		baseRows = append(baseRows, leaf(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("old-%d", i)))
	}
	for i := 10; i <= 16; i++ {
		currentRows = append(currentRows, leaf(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("new-%d", i)))
	}
	if includeNew {
		currentRows = append(currentRows, leaf("n90", protocol.RoleText, "new-node"))
	}
	return fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup, baseRows...),
		)), fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup, currentRows...),
		))
}

func TestDeltaReplaceAbsorbsRemovedInteriorWithoutDuplicateRemove(t *testing.T) {
	t.Run("closed_region_absorbs_removed_and_changed_descendants", func(t *testing.T) {
		base, current := replaceInteriorTrees(false)
		delta := mustDelta(t, "O1", base, current)
		if _, ok := delta.Replace["n2"]; !ok {
			t.Fatalf("expected n2 to be a closed replace region: %+v", delta)
		}
		if len(delta.Remove) != 0 {
			t.Fatalf("replace interior must not be repeated in remove: %v", delta.Remove)
		}
		if len(delta.Set) != 0 {
			t.Fatalf("replace interior fields must not be repeated in set: %+v", delta.Set)
		}
		mustApplyEqual(t, base, delta, current)
	})

	t.Run("new_descendant_breaks_region_closure", func(t *testing.T) {
		base, current := replaceInteriorTrees(true)
		delta := mustDelta(t, "O1", base, current)
		if _, ok := delta.Replace["n2"]; ok {
			t.Fatalf("a region with a new descendant is not closed: %+v", delta.Replace)
		}
		if got := fmt.Sprint(definitionRefs(delta.Add)); got != "[n90]" {
			t.Fatalf("new descendant add roots = %s, want [n90]", got)
		}
		requireChildBlock(t, delta, "n2", "n10", "n11", "n12", "n13", "n14", "n15", "n16", "n90")
		mustApplyEqual(t, base, delta, current)
	})
}

func movedReplaceManualDelta(current *protocol.Tree) *protocol.Delta {
	return &protocol.Delta{
		Format: "delta", Base: "O1",
		Replace: map[string]*protocol.Node{"n4": find(current, "n4").Clone()},
	}
}

func TestDeltaMovedReplaceRequiresSourceAndTargetClaims(t *testing.T) {
	base, current := movedReplaceTrees()

	onlyTarget := movedReplaceManualDelta(current)
	onlyTarget.Children = map[string][]string{"n3": {"n4"}}
	requireRejectedAtomically(t, base, onlyTarget)

	bothClaims := movedReplaceManualDelta(current)
	bothClaims.Children = map[string][]string{
		"n2": {},
		"n3": {"n4"},
	}
	got, err := Apply(base, bothClaims)
	if err != nil {
		t.Fatalf("Apply with both final claims: %v", err)
	}
	if !Equal(got, current) {
		t.Fatalf("both final claims did not move the replace root")
	}
}

func TestDeltaExistingParentCrossMoveIsNotBlanketRejected(t *testing.T) {
	base := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup,
			leaf("n3", protocol.RoleText, "one"),
			leaf("n4", protocol.RoleText, "two"),
		),
		group("n5", protocol.RoleGroup),
		group("n6", protocol.RoleGroup),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup,
			leaf("n4", protocol.RoleText, "two"),
		),
		group("n5", protocol.RoleGroup,
			leaf("n3", protocol.RoleText, "one"),
		),
		group("n6", protocol.RoleGroup),
	))

	var firstError string
	for i := 0; i < 40; i++ {
		delta := mustDelta(t, "O1", base, current)
		requireChildBlock(t, delta, "n2", "n4")
		requireChildBlock(t, delta, "n5", "n3")
		mustApplyEqual(t, base, delta, current)
		wantChildren := map[string][]string{"n2": {"n4"}, "n5": {"n3"}}
		if !reflect.DeepEqual(delta.Children, wantChildren) {
			t.Fatalf("children blocks changed with map iteration order: %+v", delta.Children)
		}

		bad := current.Root.Clone()
		bad.Children[0].Role = protocol.RoleButton
		_, err := Diff("O1", base, fullTree(bad))
		if err == nil {
			t.Fatal("role change must be rejected")
		}
		if firstError == "" {
			firstError = err.Error()
		} else if err.Error() != firstError {
			t.Fatalf("first error changed with map iteration order: %q vs %q", firstError, err.Error())
		}
	}
}

func assertFiveBoolStates(t *testing.T, node *protocol.Node, enabled, focused, selected, checked, protected *bool) {
	t.Helper()
	if !equalOptionalBool(node.Enabled, enabled) ||
		!equalOptionalBool(node.Focused, focused) ||
		!equalOptionalBool(node.Selected, selected) ||
		!equalOptionalBool(node.Checked, checked) ||
		!equalOptionalBool(node.Protected, protected) {
		t.Fatalf("five-state fields = enabled:%v focused:%v selected:%v checked:%v protected:%v, want enabled:%v focused:%v selected:%v checked:%v protected:%v",
			node.Enabled, node.Focused, node.Selected, node.Checked, node.Protected,
			enabled, focused, selected, checked, protected)
	}
}

func TestDeltaFiveBoolStateMapping(t *testing.T) {
	baseNode := leaf("n2", protocol.RoleText, "state")
	baseNode.Enabled = boolp(true)
	baseNode.Focused = boolp(false)
	baseNode.Selected = boolp(true)
	baseNode.Checked = boolp(false)
	baseNode.Protected = boolp(true)
	base := fullTree(group("root", protocol.RoleRoot, baseNode))

	currentNode := leaf("n2", protocol.RoleText, "state")
	currentNode.Enabled = nil
	currentNode.Focused = boolp(true)
	currentNode.Selected = nil
	currentNode.Checked = boolp(true)
	currentNode.Protected = boolp(false)
	current := fullTree(group("root", protocol.RoleRoot, currentNode))

	delta := mustDelta(t, "O1", base, current)
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
		t.Fatalf("set did not preserve null/false/true per field: %+v", change)
	}
	mustApplyEqual(t, base, delta, current)

	var fullBack protocol.Tree
	if err := protocol.Decode([]byte(jsonText(t, current)), &fullBack); err != nil {
		t.Fatalf("decode full tree: %v", err)
	}
	if !Equal(&fullBack, current) {
		t.Fatal("full tree round trip merged tri-state values")
	}

	replaceNode := leaf("n2", protocol.RoleText, "state")
	replaceNode.Enabled = boolp(false)
	replaceNode.Focused = nil
	replaceNode.Selected = boolp(false)
	replaceNode.Checked = boolp(true)
	replaceNode.Protected = nil
	addNode := leaf("n50", protocol.RoleText, "added")
	addNode.Enabled = nil
	addNode.Focused = boolp(false)
	addNode.Selected = boolp(true)
	addNode.Checked = nil
	addNode.Protected = boolp(true)
	manual := &protocol.Delta{
		Format: "delta", Base: "O1",
		Add:     []*protocol.Node{addNode},
		Replace: map[string]*protocol.Node{"n2": replaceNode},
		Children: map[string][]string{
			"root": {"n2", "n50"},
		},
	}
	var manualBack protocol.Delta
	if err := protocol.Decode([]byte(jsonText(t, manual)), &manualBack); err != nil {
		t.Fatalf("decode delta definitions: %v", err)
	}
	got, err := Apply(base, &manualBack)
	if err != nil {
		t.Fatalf("Apply definitions: %v", err)
	}
	assertFiveBoolStates(t, find(got, "n2"),
		replaceNode.Enabled, replaceNode.Focused, replaceNode.Selected,
		replaceNode.Checked, replaceNode.Protected)
	assertFiveBoolStates(t, find(got, "n50"),
		addNode.Enabled, addNode.Focused, addNode.Selected,
		addNode.Checked, addNode.Protected)
}

func TestDeltaHighestRemovedAncestorAcrossSurvivingMiddle(t *testing.T) {
	base := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup,
			group("n3", protocol.RoleGroup,
				leaf("n4", protocol.RoleText, "gone"),
			),
		),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		group("n3", protocol.RoleGroup),
	))

	delta := mustDelta(t, "O1", base, current)
	if fmt.Sprint(delta.Remove) != "[n2]" {
		t.Fatalf("remove = %v, want only the highest removed ancestor n2", delta.Remove)
	}
	requireChildBlock(t, delta, "root", "n3")
	requireChildBlock(t, delta, "n3")
	mustApplyEqual(t, base, delta, current)
}

func TestDeltaReplaceClosurePropagatesDeepParentChange(t *testing.T) {
	var bRows, cRows []*protocol.Node
	for i := 10; i <= 15; i++ {
		bRows = append(bRows, leaf(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("b-%d", i)))
		cRows = append(cRows, leaf(fmt.Sprintf("n%d", i+10), protocol.RoleText, fmt.Sprintf("c-%d", i)))
	}
	base := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup,
			group("n3", protocol.RoleGroup, bRows...),
			group("n4", protocol.RoleGroup, cRows...),
		),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup,
			group("n3", protocol.RoleGroup,
				append(append([]*protocol.Node{}, bRows...), group("n4", protocol.RoleGroup, cRows...))...,
			),
		),
	))

	delta := mustDelta(t, "O1", base, current)
	if len(delta.Replace) != 0 {
		t.Fatalf("deep parent change must break replace closure: %+v", delta.Replace)
	}
	requireChildBlock(t, delta, "n2", "n3")
	requireChildBlock(t, delta, "n3", "n10", "n11", "n12", "n13", "n14", "n15", "n4")
	mustApplyEqual(t, base, delta, current)
}

func TestDeltaRemoveRootAndDeadParentClaimsRejected(t *testing.T) {
	t.Run("remove_root_cannot_be_revived", func(t *testing.T) {
		base := fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup),
		))
		delta := &protocol.Delta{
			Format: "delta", Base: "O1", Remove: []string{"n2"},
			Children: map[string][]string{"root": {"n2"}},
		}
		requireRejectedAtomically(t, base, delta)
	})

	t.Run("dead_parent_cannot_publish_children", func(t *testing.T) {
		base := fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup),
		))
		delta := &protocol.Delta{
			Format: "delta", Base: "O1", Remove: []string{"n2"},
			Children: map[string][]string{"n2": {}},
		}
		requireRejectedAtomically(t, base, delta)
	})

	t.Run("unreachable_explicit_cycle_does_not_self_support", func(t *testing.T) {
		base := fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup),
			group("n3", protocol.RoleGroup),
		))
		delta := &protocol.Delta{
			Format: "delta", Base: "O1",
			Add: []*protocol.Node{group("n4", protocol.RoleGroup)},
			Children: map[string][]string{
				"root": {"n2"},
				"n3":   {"n4"},
				"n4":   {"n3"},
			},
		}
		requireRejectedAtomically(t, base, delta)
	})

	t.Run("isolated_replace_is_not_a_second_root", func(t *testing.T) {
		base := fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup,
				group("n3", protocol.RoleGroup),
			),
		))
		delta := &protocol.Delta{
			Format: "delta", Base: "O1",
			Replace: map[string]*protocol.Node{
				"n3": group("n3", protocol.RoleGroup),
			},
			Children: map[string][]string{"root": {"n2"}},
		}
		delta.Children["n2"] = []string{}
		requireRejectedAtomically(t, base, delta)
	})

	t.Run("isolated_add_is_not_a_second_root", func(t *testing.T) {
		base := fullTree(group("root", protocol.RoleRoot,
			group("n2", protocol.RoleGroup),
		))
		delta := &protocol.Delta{
			Format: "delta", Base: "O1",
			Add: []*protocol.Node{leaf("n50", protocol.RoleText, "orphan")},
		}
		requireRejectedAtomically(t, base, delta)
	})
}

func escapingReplaceTrees() (*protocol.Tree, *protocol.Tree) {
	var oldRows, newRows []*protocol.Node
	for i := 10; i <= 17; i++ {
		oldRows = append(oldRows, leaf(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("old-%d", i)))
		newRows = append(newRows, leaf(fmt.Sprintf("n%d", i), protocol.RoleText, fmt.Sprintf("new-%d", i)))
	}
	base := fullTree(group("root", protocol.RoleRoot,
		group("n2", protocol.RoleGroup,
			group("n3", protocol.RoleGroup,
				group("n4", protocol.RoleGroup, oldRows...),
			),
		),
	))
	current := fullTree(group("root", protocol.RoleRoot,
		group("n3", protocol.RoleGroup,
			group("n4", protocol.RoleGroup, newRows...),
		),
	))
	return base, current
}

func TestDeltaReplaceSurvivesThroughEscapingAncestor(t *testing.T) {
	base, current := escapingReplaceTrees()
	delta := mustDelta(t, "O1", base, current)
	if fmt.Sprint(delta.Remove) != "[n2]" {
		t.Fatalf("remove = %v, want the removed outer container n2", delta.Remove)
	}
	if _, ok := delta.Replace["n4"]; !ok {
		if _, enclosing := delta.Replace["n3"]; !enclosing {
			t.Fatalf("replace must survive inside the old removed closure: %+v", delta.Replace)
		}
	}
	requireChildBlock(t, delta, "root", "n3")
	if _, ok := delta.Children["n3"]; ok {
		t.Fatalf("unchanged inherited n3->n4 edge needs no explicit block: %+v", delta.Children)
	}
	mustApplyEqual(t, base, delta, current)
}

func TestDiffApplyRandomStructuralRewrite(t *testing.T) {
	roleFor := func(ref int) protocol.Role {
		if ref%2 == 0 {
			return protocol.RoleGroup
		}
		return protocol.RoleText
	}
	shuffleChildren := func(root *protocol.Node, r *rand.Rand) {
		var walk func(*protocol.Node)
		walk = func(node *protocol.Node) {
			r.Shuffle(len(node.Children), func(i, j int) {
				node.Children[i], node.Children[j] = node.Children[j], node.Children[i]
			})
			for _, child := range node.Children {
				walk(child)
			}
		}
		walk(root)
	}

	for seed := int64(0); seed < 100; seed++ {
		r := rand.New(rand.NewSource(seed))
		baseNodes := map[string]*protocol.Node{
			"n1": {Ref: "n1", Role: protocol.RoleRoot},
		}
		baseRefs := []string{"n1"}
		for ref := 2; ref <= 20; ref++ {
			name := fmt.Sprintf("n%d", ref)
			node := &protocol.Node{Ref: name, Role: roleFor(ref)}
			parent := baseRefs[r.Intn(len(baseRefs))]
			baseNodes[parent].Children = append(baseNodes[parent].Children, node)
			baseNodes[name] = node
			baseRefs = append(baseRefs, name)
		}
		shuffleChildren(baseNodes["n1"], r)

		currentNodes := map[string]*protocol.Node{
			"n1": {Ref: "n1", Role: protocol.RoleRoot},
		}
		currentRefs := []string{"n1"}
		for ref := 2; ref <= 25; ref++ {
			name := fmt.Sprintf("n%d", ref)
			if ref <= 20 && r.Intn(5) == 0 {
				continue
			}
			node := &protocol.Node{Ref: name}
			switch {
			case ref <= 20:
				node.Role = roleFor(ref)
				if r.Intn(3) == 0 {
					node.Label = []string{fmt.Sprintf("changed-%d", seed)}
				}
			case ref%2 == 0:
				node.Role = protocol.RoleGroup
			default:
				node.Role = protocol.RoleText
				node.Label = []string{fmt.Sprintf("new-%d", ref)}
			}
			parent := currentRefs[r.Intn(len(currentRefs))]
			currentNodes[parent].Children = append(currentNodes[parent].Children, node)
			currentNodes[name] = node
			currentRefs = append(currentRefs, name)
		}
		shuffleChildren(currentNodes["n1"], r)

		base := fullTree(baseNodes["n1"])
		current := fullTree(currentNodes["n1"])
		delta := mustDelta(t, "O1", base, current)
		mustApplyEqual(t, base, delta, current)
	}
}
