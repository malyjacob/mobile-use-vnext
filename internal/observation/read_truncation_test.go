package observation

import (
	"errors"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestCompactActionableLabelTruncation(t *testing.T) {
	limit := strings.Repeat("中", 250)
	longText := strings.Repeat("文", 4000)
	longHoisted := strings.Repeat("歌", 350)
	spacePrefix := strings.Repeat("多", 249) + " "
	invalidPrefix := strings.Repeat("多", 248) + "\xff字"
	compositePrefix := strings.Repeat("甲", 249) + "乙"
	compositeTail := "后半段文本留存呀哦啊"

	button := func(ref string, parts ...string) *protocol.Node {
		node := actionNodeT(ref, protocol.RoleButton, rectT(0, 0, 300, 100), protocol.ActionTap)
		node.Label = parts
		return node
	}
	exact := button("n2", limit)
	long := button("n3", limit+"长", "第二段", spacePrefix+"末", invalidPrefix+"尾")
	passive := nodeT("n4", protocol.RoleText, rectT(0, 0, 300, 100), longText)
	input := actionNodeT("n5", protocol.RoleInput, rectT(0, 0, 300, 100), protocol.ActionText)
	input.Label = []string{longText}
	input.Value = readString(" " + longText + " ")
	shell := actionNodeT("n6", protocol.RoleView, rectT(0, 0, 300, 100), protocol.ActionTap)
	shell.Children = []*protocol.Node{nodeT("n7", protocol.RoleText, rectT(0, 0, 300, 100), longHoisted)}
	composite := button("n8", compositePrefix+compositeTail)
	composite.Children = []*protocol.Node{
		nodeT("n9", protocol.RoleText, rectT(0, 0, 300, 100), "乙"),
		nodeT("n10", protocol.RoleText, rectT(0, 0, 300, 100), "本留存呀哦啊"),
	}
	// Synthetic context node exercises the explicit Context guard even with
	// actions and a label. Builder-produced context nodes carry neither.
	context := button("n11", longText)
	context.Context = true

	tree := rootT(exact, long, passive, input, shell, composite, context)
	compact := ProjectCompactTree(tree)
	cases := []struct {
		ref       string
		role      protocol.Role
		label     []string
		truncated bool
	}{
		{"n2", protocol.RoleButton, []string{limit}, false},
		{"n3", protocol.RoleButton, []string{limit, "第二段", spacePrefix, invalidPrefix}, true},
		{"n4", protocol.RoleText, []string{longText}, false},
		{"n5", protocol.RoleInput, []string{longText}, false},
		{"n6", protocol.RoleButton, []string{longHoisted}, false},
		{"n8", protocol.RoleButton, []string{compositePrefix}, true},
		{"n11", protocol.RoleButton, []string{longText}, false},
	}
	for _, tc := range cases {
		node := childOf(t, compact.Root, tc.ref)
		if node == nil {
			t.Fatalf("compact %s is missing", tc.ref)
		}
		if node.Role != tc.role || node.Truncated != tc.truncated || !equalStrings(node.Label, tc.label) {
			t.Errorf("compact %s: role=%s (want %s), truncated=%t (want %t), label matches=%t",
				tc.ref, node.Role, tc.role, node.Truncated, tc.truncated, equalStrings(node.Label, tc.label))
		}
	}
	if node := childOf(t, compact.Root, "n5"); node == nil || node.Value == nil || *node.Value != *input.Value {
		t.Fatal("compact input must retain its raw value")
	}
	requireImmediateRefs(t, childOf(t, compact.Root, "n6"))
	requireImmediateRefs(t, childOf(t, compact.Root, "n8"), "n10")

	detailed := ProjectDetailedTree(tree)
	for _, original := range []*protocol.Node{exact, long, passive, input, shell, composite, context} {
		node := childOf(t, detailed.Root, original.Ref)
		if node == nil || node.Truncated || node.Role != original.Role || !equalStrings(node.Label, original.Label) {
			t.Errorf("detailed %s must preserve the original role and label: %+v", original.Ref, node)
		}
	}
	if long.Label[0] != limit+"长" || long.Label[2] != spacePrefix+"末" || long.Label[3] != invalidPrefix+"尾" ||
		composite.Label[0] != compositePrefix+compositeTail {
		t.Fatal("compact projection modified the original label")
	}
	requireImmediateRefs(t, childOf(t, detailed.Root, "n6"), "n7")
	requireImmediateRefs(t, childOf(t, detailed.Root, "n8"), "n9", "n10")
}

func TestReadDeltaTruncationTransitions(t *testing.T) {
	prefix := strings.Repeat("中", 250)
	project := func(label string) *protocol.ReadTree {
		button := actionNodeT("n2", protocol.RoleButton, rectT(0, 0, 300, 100), protocol.ActionTap)
		button.Label = []string{label}
		return ProjectCompactTree(rootT(button))
	}
	exact, truncated := project(prefix), project(prefix+"尾")
	if ReadEqual(exact, truncated) {
		t.Error("ReadEqual must compare Truncated even on a child with identical delivered fields")
	}
	for _, tc := range []struct {
		name       string
		base, next *protocol.ReadTree
		want       bool
	}{
		{"false to true", exact, truncated, true},
		{"true to false", truncated, exact, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delta, err := ReadDiff("O1", tc.base, tc.next)
			if err != nil {
				t.Fatal(err)
			}
			set := delta.Set["n2"]
			if !set.Truncated.Set || set.Truncated.Value != tc.want || set.Label.Set || len(delta.Set) != 1 {
				t.Fatalf("expected only truncated=%v on n2, got %+v", tc.want, delta)
			}
			rebuilt, err := ReadApply(tc.base, mustReadRoundTrip(t, delta))
			if err != nil || !ReadEqual(rebuilt, tc.next) || rebuilt.Root.Children[0].Truncated != tc.want {
				t.Fatalf("apply must reconstruct truncated=%v: %+v, %v", tc.want, rebuilt, err)
			}
		})
	}
	delta, err := ReadDiff("O1", truncated, project(prefix+"另一段尾部"))
	if err != nil || !delta.IsEmpty() {
		t.Fatalf("a change past the prefix must yield an empty delta: %+v, %v", delta, err)
	}
}

func TestReadApplyRejectsRootTruncationSet(t *testing.T) {
	base := ProjectCompactTree(rootT())
	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			base.Root.Ref: {Truncated: protocol.Field[bool]{Set: true, Value: false}},
		},
	}
	if _, err := ReadApply(base, delta); !errors.Is(err, ErrNotRepresentable) {
		t.Fatalf("ReadApply must reject any root Truncated set: %v", err)
	}
}
