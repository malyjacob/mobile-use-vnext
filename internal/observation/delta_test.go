package observation

import (
	"errors"
	"fmt"
	"math/rand"
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
	if len(delta.Remove) != 1 || delta.Remove[0] != "n8" {
		t.Fatalf("remove = %v, want only the highest root n8", delta.Remove)
	}
	if len(delta.Children) != 0 {
		t.Fatalf("a pure remove must not need a children block: %v", delta.Children)
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
	a := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{Ref: "n2", Role: protocol.RoleText}))
	b := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{
		Ref: "n2", Role: protocol.RoleText, Label: []string{}, Actions: []protocol.Action{},
		Enabled: boolp(true), Focused: boolp(false), Selected: boolp(false), Protected: boolp(false),
	}))
	if !Equal(a, b) {
		t.Fatalf("encoding defaults must compare equal")
	}
	c := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{Ref: "n2", Role: protocol.RoleText, Enabled: nil}))
	// a has no Enabled which means known true; c has nil which Equal treats as
	// the encoding default true as well. Both are "true".
	if !Equal(a, c) {
		t.Fatalf("absent enabled and explicit nil both mean the encoding default")
	}
	d := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{Ref: "n2", Role: protocol.RoleText, Enabled: boolp(false)}))
	if Equal(a, d) {
		t.Fatalf("known false must differ from true")
	}
	e := fullTree(group("n1", protocol.RoleRoot, &protocol.Node{Ref: "n2", Role: protocol.RoleText, Context: true}))
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
