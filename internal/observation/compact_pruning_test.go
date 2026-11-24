package observation

import (
	"fmt"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func compactScreenTree(children ...*protocol.Node) *protocol.Tree {
	tree := rootT(children...)
	tree.Root.Bounds = rectT(0, 0, 1000, 1000)
	return tree
}

func requireNoReadChains(t *testing.T, node *protocol.ReadNode) {
	t.Helper()
	if node == nil {
		return
	}
	if len(node.Chain) != 0 {
		t.Fatalf("unexpected historical chain %v at refs %v", node.Chain, node.Refs())
	}
	for _, child := range node.Children {
		requireNoReadChains(t, child)
	}
}

func requireNoDeltaChains(t *testing.T, delta *protocol.ReadDelta) {
	t.Helper()
	for _, node := range delta.Add {
		requireNoReadChains(t, node)
	}
	for _, node := range delta.Replace {
		requireNoReadChains(t, node)
	}
}

func immediateRefs(node *protocol.ReadNode) []string {
	if node == nil {
		return nil
	}
	refs := make([]string, 0, len(node.Children))
	for _, child := range node.Children {
		refs = append(refs, child.Refs()...)
	}
	return refs
}

func requireImmediateRefs(t *testing.T, node *protocol.ReadNode, want ...string) {
	t.Helper()
	if got := fmt.Sprint(immediateRefs(node)); got != fmt.Sprint(want) {
		t.Fatalf("child refs = %s, want %v", got, want)
	}
}

func TestCompactPruningImageCriteria(t *testing.T) {
	t.Run("unlabeled non-interactive image leaves are always pruned", func(t *testing.T) {
		// An unlabeled image leaf with no action and no non-default state carries
		// nothing a reader can use: the pixels are not delivered and there is
		// nothing to aim at. Neither its area nor a screen denominator matters.
		for _, bounds := range []*protocol.Bounds{
			rectT(0, 0, 1000, 1000),
			rectT(0, 0, 500, 500),
			rectT(10, 10, 100, 100),
			rectT(0, 0, 1, 1),
			nil,
		} {
			image := nodeT("n2", protocol.RoleImage, bounds)
			compact := ProjectCompactTree(compactScreenTree(image))
			if node := childOf(t, compact.Root, "n2"); node != nil {
				t.Fatalf("unlabeled non-interactive image %v must be pruned: %+v", bounds, node)
			}
		}
	})

	t.Run("labelled interactive or non-default images are retained", func(t *testing.T) {
		cases := map[string]func(*protocol.Node){
			"label":           func(n *protocol.Node) { n.Label = []string{"icon"} },
			"tap":             func(n *protocol.Node) { n.Actions = []protocol.Action{protocol.ActionTap} },
			"long_press":      func(n *protocol.Node) { n.Actions = []protocol.Action{protocol.ActionLongPress} },
			"value":           func(n *protocol.Node) { n.Value = readString("v") },
			"hint_empty":      func(n *protocol.Node) { n.Hint = readString("") },
			"resource_empty":  func(n *protocol.Node) { n.Resource = readString("") },
			"axis":            func(n *protocol.Node) { n.Axis = protocol.AxisY },
			"context":         func(n *protocol.Node) { n.Context = true },
			"non_image_role":  func(n *protocol.Node) { n.Role = protocol.RoleGroup },
			"enabled_unknown": func(n *protocol.Node) { n.Enabled = nil },
			"enabled_false":   func(n *protocol.Node) { n.Enabled = readBool(false) },
			"focused_unknown": func(n *protocol.Node) { n.Focused = nil },
			"focused_true":    func(n *protocol.Node) { n.Focused = readBool(true) },
			"selected_unknown": func(n *protocol.Node) {
				n.Selected = nil
			},
			"selected_true": func(n *protocol.Node) { n.Selected = readBool(true) },
			"protected_unknown": func(n *protocol.Node) {
				n.Protected = nil
			},
			"protected_true": func(n *protocol.Node) { n.Protected = readBool(true) },
			"checked_unknown": func(n *protocol.Node) {
				n.Checked = nil
			},
			"checked_true": func(n *protocol.Node) { n.Checked = readBool(true) },
		}
		for name, mutate := range cases {
			t.Run(name, func(t *testing.T) {
				image := nodeT("n2", protocol.RoleImage, rectT(0, 0, 100, 100))
				mutate(image)
				compact := ProjectCompactTree(compactScreenTree(image))
				if childOf(t, compact.Root, "n2") == nil {
					t.Fatal("image must remain when a required public condition is absent")
				}
			})
		}
	})

	t.Run("unknown screen does not disable the image rule", func(t *testing.T) {
		tree := rootT(nodeT("n2", protocol.RoleImage, rectT(0, 0, 1000, 1000)))
		tree.Root.Bounds = nil
		if node := childOf(t, ProjectCompactTree(tree).Root, "n2"); node != nil {
			t.Fatalf("unlabeled image must be pruned without a screen rectangle: %+v", node)
		}
	})

	t.Run("wrapper left empty by a pruned image disappears", func(t *testing.T) {
		wrapper := plainT("n2", protocol.RoleView)
		wrapper.Children = []*protocol.Node{
			nodeT("n3", protocol.RoleImage, rectT(0, 0, 1000, 1000)),
		}
		compact := ProjectCompactTree(compactScreenTree(wrapper))
		requireImmediateRefs(t, compact.Root)
	})

	t.Run("image children make a non-leaf", func(t *testing.T) {
		image := nodeT("n2", protocol.RoleImage, rectT(0, 0, 10, 10))
		image.Children = []*protocol.Node{
			nodeT("n3", protocol.RoleText, rectT(0, 0, 5, 5), "caption"),
		}
		compact := ProjectCompactTree(compactScreenTree(image))
		if childOf(t, compact.Root, "n2") == nil {
			t.Fatal("an image with an original child must remain")
		}
		if childOf(t, compact.Root, "n3") == nil {
			t.Fatal("the image child must remain")
		}
	})

	t.Run("non-leaf image remains after its only child is pruned", func(t *testing.T) {
		image := nodeT("n2", protocol.RoleImage, rectT(0, 0, 10, 10))
		image.Children = []*protocol.Node{
			nodeT("n3", protocol.RoleImage, rectT(20, 20, 30, 30)),
		}
		compact := ProjectCompactTree(compactScreenTree(image))
		parent := childOf(t, compact.Root, "n2")
		if parent == nil || len(parent.Children) != 0 {
			t.Fatalf("original non-leaf image must remain empty: %+v", parent)
		}
		if childOf(t, compact.Root, "n3") != nil {
			t.Fatal("the unlabeled image child must still be pruned")
		}
	})

	t.Run("detailed view keeps every image", func(t *testing.T) {
		unlabeled := nodeT("n2", protocol.RoleImage, rectT(0, 0, 1000, 1000))
		labelled := nodeT("n3", protocol.RoleImage, rectT(0, 0, 100, 100), "cover")
		detailed := ProjectDetailedTree(compactScreenTree(unlabeled, labelled))
		for _, ref := range []string{"n2", "n3"} {
			node := childOf(t, detailed.Root, ref)
			if node == nil || node.Bounds.Kind != protocol.GeometryKnown {
				t.Fatalf("detailed view must keep image %s: %+v", ref, node)
			}
		}
	})
}

func TestCompactPruningEmptyAnonymousContainers(t *testing.T) {
	t.Run("continuous empty wrappers", func(t *testing.T) {
		image := nodeT("n4", protocol.RoleImage, rectT(0, 0, 10, 10))
		inner := plainT("n3", protocol.RoleView)
		inner.Children = []*protocol.Node{image}
		outer := plainT("n2", protocol.RoleView)
		outer.Children = []*protocol.Node{inner}

		compact := ProjectCompactTree(compactScreenTree(outer))
		requireImmediateRefs(t, compact.Root)
	})

	t.Run("original empty leaf stays", func(t *testing.T) {
		compact := ProjectCompactTree(compactScreenTree(plainT("n2", protocol.RoleView)))
		requireBoundsKind(t, compact.Root, "n2", protocol.GeometryHidden)
		requireImmediateRefs(t, compact.Root, "n2")
	})

	t.Run("container with remaining text is bypassed", func(t *testing.T) {
		container := plainT("n2", protocol.RoleView)
		container.Children = []*protocol.Node{
			nodeT("n3", protocol.RoleImage, rectT(0, 0, 10, 10)),
			nodeT("n4", protocol.RoleText, rectT(100, 100, 200, 200), "keep"),
		}
		compact := ProjectCompactTree(compactScreenTree(container))
		if childOf(t, compact.Root, "n2") != nil {
			t.Fatal("the now-singleton anonymous container must be bypassed")
		}
		requireBoundsKind(t, compact.Root, "n4", protocol.GeometryHidden)
		requireImmediateRefs(t, compact.Root, "n4")
	})

	t.Run("two images leave a container empty", func(t *testing.T) {
		container := plainT("n2", protocol.RoleGroup)
		container.Children = []*protocol.Node{
			nodeT("n3", protocol.RoleImage, rectT(0, 0, 10, 10)),
			nodeT("n4", protocol.RoleImage, rectT(20, 20, 30, 30)),
		}
		compact := ProjectCompactTree(compactScreenTree(container))
		requireImmediateRefs(t, compact.Root)
	})

	t.Run("non-cleanable empty containers stay", func(t *testing.T) {
		context := plainT("n2", protocol.RoleGroup)
		context.Context = true
		context.Children = []*protocol.Node{
			nodeT("n3", protocol.RoleImage, rectT(0, 0, 10, 10)),
		}
		label := nodeT("n4", protocol.RoleGroup, rectT(0, 200, 400, 400), "keep")
		label.Children = []*protocol.Node{
			nodeT("n5", protocol.RoleImage, rectT(0, 210, 10, 220)),
		}
		action := actionNodeT("n6", protocol.RoleView, rectT(0, 450, 400, 650), protocol.ActionTap)
		action.Children = []*protocol.Node{
			nodeT("n7", protocol.RoleImage, rectT(0, 460, 10, 470)),
		}
		unknown := plainT("n8", protocol.RoleView)
		unknown.Selected = nil
		unknown.Children = []*protocol.Node{
			nodeT("n9", protocol.RoleImage, rectT(0, 700, 10, 710)),
		}

		compact := ProjectCompactTree(compactScreenTree(context, label, action, unknown))
		for _, ref := range []string{"n2", "n4", "n6", "n8"} {
			node := childOf(t, compact.Root, ref)
			if node == nil || len(node.Children) != 0 {
				t.Fatalf("non-cleanable container %s must remain empty: %+v", ref, node)
			}
		}
	})
}

func TestCompactPruningSingletonWrapperShapes(t *testing.T) {
	t.Run("tail image and button", func(t *testing.T) {
		button := actionNodeT("n11", protocol.RoleButton, rectT(100, 100, 300, 200), protocol.ActionTap)
		button.Label = []string{"open"}
		tail := plainT("n9", protocol.RoleView)
		tail.Children = []*protocol.Node{
			nodeT("n10", protocol.RoleImage, rectT(10, 110, 20, 120)),
			button,
		}
		mid := plainT("n8", protocol.RoleView)
		mid.Children = []*protocol.Node{tail}
		head := plainT("n7", protocol.RoleView)
		head.Children = []*protocol.Node{mid}

		compact := ProjectCompactTree(compactScreenTree(head))
		requireImmediateRefs(t, compact.Root, "n11")
	})

	t.Run("clickable card and inner button", func(t *testing.T) {
		button := actionNodeT("n11", protocol.RoleButton, rectT(100, 300, 300, 400), protocol.ActionTap)
		button.Label = []string{"inner"}
		card := actionNodeT("n9", protocol.RoleView, rectT(0, 200, 400, 500), protocol.ActionTap)
		card.Label = []string{"card"}
		card.Children = []*protocol.Node{
			nodeT("n10", protocol.RoleImage, rectT(10, 210, 20, 220)),
			button,
		}
		mid := plainT("n8", protocol.RoleGroup)
		mid.Children = []*protocol.Node{card}
		head := plainT("n7", protocol.RoleView)
		head.Children = []*protocol.Node{mid}

		compact := ProjectCompactTree(compactScreenTree(head))
		requireImmediateRefs(t, compact.Root, "n9")
		cardRead := childOf(t, compact.Root, "n9")
		requireImmediateRefs(t, cardRead, "n11")
	})

	t.Run("multilevel branches flatten in preorder", func(t *testing.T) {
		first := actionNodeT("n15", protocol.RoleButton, rectT(0, 0, 200, 100), protocol.ActionTap)
		first.Label = []string{"first"}
		second := actionNodeT("n16", protocol.RoleButton, rectT(0, 120, 200, 220), protocol.ActionLongPress)
		second.Label = []string{"second"}
		branch := plainT("n14", protocol.RoleGroup)
		branch.Children = []*protocol.Node{first, second}
		third := nodeT("n17", protocol.RoleText, rectT(0, 240, 200, 300), "third")
		fourth := nodeT("n18", protocol.RoleText, rectT(0, 320, 200, 380), "fourth")
		otherBranch := plainT("n13", protocol.RoleView)
		otherBranch.Children = []*protocol.Node{third, fourth}
		outer := plainT("n12", protocol.RoleGroup)
		outer.Children = []*protocol.Node{branch, otherBranch}

		compact := ProjectCompactTree(compactScreenTree(outer))
		requireImmediateRefs(t, compact.Root, "n15", "n16", "n17", "n18")
	})

	t.Run("wrappers between interaction nodes", func(t *testing.T) {
		inner := actionNodeT("n23", protocol.RoleButton, rectT(100, 100, 300, 200), protocol.ActionTap)
		inner.Label = []string{"inner"}
		inner.Children = []*protocol.Node{
			nodeT("n24", protocol.RoleImage, rectT(110, 110, 120, 120)),
		}
		group := plainT("n22", protocol.RoleGroup)
		group.Children = []*protocol.Node{inner}
		view := plainT("n21", protocol.RoleView)
		view.Children = []*protocol.Node{group}
		card := actionNodeT("n20", protocol.RoleView, rectT(0, 0, 400, 300), protocol.ActionTap)
		card.Label = []string{"card"}
		card.Children = []*protocol.Node{view}

		compact := ProjectCompactTree(compactScreenTree(card))
		requireImmediateRefs(t, compact.Root, "n20")
		cardRead := childOf(t, compact.Root, "n20")
		requireImmediateRefs(t, cardRead, "n23")
		if childOf(t, compact.Root, "n24") != nil {
			t.Fatal("the small image under the inner action must be omitted")
		}
	})

	t.Run("wrapper replacement preserves sibling order", func(t *testing.T) {
		first := nodeT("n2", protocol.RoleText, rectT(0, 0, 200, 100), "first")
		button := actionNodeT("n5", protocol.RoleButton, rectT(0, 150, 200, 250), protocol.ActionTap)
		button.Label = []string{"middle"}
		wrapper := plainT("n4", protocol.RoleView)
		wrapper.Children = []*protocol.Node{button}
		last := nodeT("n6", protocol.RoleText, rectT(0, 300, 200, 400), "last")

		compact := ProjectCompactTree(compactScreenTree(first, wrapper, last))
		requireImmediateRefs(t, compact.Root, "n2", "n5", "n6")
	})

	t.Run("unknown and zero-child nodes keep wrapper boundaries", func(t *testing.T) {
		empty := plainT("n2", protocol.RoleView)
		context := plainT("n3", protocol.RoleGroup)
		context.Context = true
		context.Bounds = nil
		context.Children = []*protocol.Node{
			nodeT("n8", protocol.RoleText, rectT(0, 0, 200, 60), "context child"),
			nodeT("n9", protocol.RoleText, rectT(0, 60, 200, 120), "another context child"),
		}
		selected := plainT("n4", protocol.RoleView)
		selected.Bounds = rectT(0, 0, 1000, 1000)
		selected.Selected = readBool(true)
		selected.Children = []*protocol.Node{
			nodeT("n10", protocol.RoleText, rectT(0, 120, 200, 180), "selected child"),
			nodeT("n11", protocol.RoleText, rectT(0, 180, 200, 240), "another selected child"),
		}
		unknownBounds := nodeT("n5", protocol.RoleView, nil)
		button := actionNodeT("n7", protocol.RoleButton, rectT(0, 500, 200, 600), protocol.ActionTap)
		button.Label = []string{"button"}
		wrapper := plainT("n6", protocol.RoleGroup)
		wrapper.Children = []*protocol.Node{button}

		compact := ProjectCompactTree(compactScreenTree(
			empty, context, selected, unknownBounds, wrapper,
		))
		requireImmediateRefs(t, compact.Root, "n2", "n3", "n4", "n5", "n7")
		requireImmediateRefs(t, childOf(t, compact.Root, "n3"), "n8", "n9")
		requireImmediateRefs(t, childOf(t, compact.Root, "n4"), "n10", "n11")
		requireBoundsKind(t, compact.Root, "n4", protocol.GeometryHidden)
	})
}

func TestCompactPruningKeepsDetailedTreeAndActionEvidence(t *testing.T) {
	image := nodeT("n3", protocol.RoleImage, rectT(10, 10, 20, 20))
	button := actionNodeT("n4", protocol.RoleButton, rectT(100, 100, 300, 200), protocol.ActionTap)
	button.Label = []string{"open"}
	wrapper := plainT("n2", protocol.RoleView)
	wrapper.Children = []*protocol.Node{image, button}
	tree := compactScreenTree(wrapper)
	internalBefore := readWireText(t, tree)

	compact := ProjectCompactTree(tree)
	requireNoReadChains(t, compact.Root)
	requireImmediateRefs(t, compact.Root, "n4")
	if childOf(t, compact.Root, "n3") != nil {
		t.Fatal("compact delivery must omit the small image")
	}
	if internalBefore != readWireText(t, tree) {
		t.Fatal("compact pruning mutated the complete internal tree")
	}

	detailed := ProjectDetailedTree(tree)
	requireNoReadChains(t, detailed.Root)
	requireImmediateRefs(t, detailed.Root, "n2")
	detailedWrapper := childOf(t, detailed.Root, "n2")
	requireImmediateRefs(t, detailedWrapper, "n3", "n4")
	for _, ref := range []string{"n2", "n3", "n4"} {
		node := childOf(t, detailed.Root, ref)
		if node == nil || node.Bounds.Kind != protocol.GeometryKnown {
			t.Fatalf("detailed view must retain known geometry for %s: %+v", ref, node)
		}
	}
	if imageRead := childOf(t, detailed.Root, "n3"); imageRead.Bounds.Rect != *image.Bounds {
		t.Fatalf("detailed image bounds = %+v, want %+v", imageRead.Bounds.Rect, *image.Bounds)
	}
	if buttonRead := childOf(t, detailed.Root, "n4"); len(buttonRead.Actions) != 1 ||
		buttonRead.Actions[0] != protocol.ActionTap {
		t.Fatalf("detailed action evidence changed: %+v", buttonRead)
	}
}

func compactDiffFrame(imagePresent, imageMoved bool, imageBounds *protocol.Bounds) *protocol.ReadTree {
	container := nodeT("n2", protocol.RoleGroup, rectT(0, 0, 1000, 700), "content")
	other := nodeT("n5", protocol.RoleGroup, rectT(0, 720, 1000, 1000), "other")
	button := actionNodeT("n4", protocol.RoleButton, rectT(600, 50, 900, 150), protocol.ActionTap)
	button.Label = []string{"action"}
	// A labelled image is content, so the compact view retains it and only the
	// frame that really reports it carries the node.
	image := nodeT("n3", protocol.RoleImage, imageBounds, "cover")

	if imagePresent {
		if imageMoved {
			other.Children = []*protocol.Node{image}
		} else {
			container.Children = append(container.Children, image)
		}
	}
	if !imageMoved {
		container.Children = append(container.Children, button)
	}
	return ProjectCompactTree(compactScreenTree(container, other))
}

func TestCompactPruningNewDeltaRoundTrip(t *testing.T) {
	base := compactDiffFrame(false, false, rectT(10, 50, 20, 60))
	reappeared := compactDiffFrame(true, false, rectT(100, 100, 300, 300))
	moved := compactDiffFrame(true, true, rectT(100, 100, 300, 300))
	if childOf(t, base.Root, "n3") != nil {
		t.Fatal("base frame must omit the absent image")
	}
	if childOf(t, reappeared.Root, "n3") == nil {
		t.Fatal("reappeared frame must retain the labelled image")
	}
	movedParent := childOf(t, moved.Root, "n5")
	if movedParent == nil || len(movedParent.Children) != 1 ||
		movedParent.Children[0].Ref != "n3" {
		t.Fatalf("moved image must use the same ref below n5: %+v", movedParent)
	}

	for _, current := range []*protocol.ReadTree{reappeared, moved} {
		delta, err := ReadDiff("O1", base, current)
		if err != nil {
			t.Fatalf("ReadDiff: %v", err)
		}
		if delta.Base != "O1" || delta.IsEmpty() {
			t.Fatalf("delta header/body = %+v", delta)
		}
		requireNoDeltaChains(t, delta)

		decoded := mustReadRoundTrip(t, delta)
		mustReadApplyEqual(t, base, decoded, current)
		applied, err := ReadApply(base, decoded)
		if err != nil {
			t.Fatalf("ReadApply: %v", err)
		}
		requireNoReadChains(t, applied.Root)

		var decodedFull protocol.ReadTree
		if err := protocolDecode([]byte(readWireText(t, current)), &decodedFull); err != nil {
			t.Fatalf("decode current full: %v", err)
		}
		if !ReadEqual(applied, &decodedFull) {
			t.Fatal("strictly decoded current full differs from applied delta")
		}
	}

	addDelta, err := ReadDiff("O1", base, reappeared)
	if err != nil {
		t.Fatalf("reappearance ReadDiff: %v", err)
	}
	if len(addDelta.Add) == 0 {
		t.Fatalf("reappearance must exercise add definitions: %+v", addDelta)
	}
}

func TestCompactPruningReplaceDefinitionsAreOrdinary(t *testing.T) {
	base, current := movedReadReplaceTrees()
	requireNoReadChains(t, base.Root)
	requireNoReadChains(t, current.Root)

	delta := mustReadDelta(t, "O1", base, current)
	if len(delta.Replace) == 0 {
		t.Fatalf("fixture must exercise replace definitions: %+v", delta)
	}
	requireNoDeltaChains(t, delta)
	mustReadApplyEqual(t, base, delta, current)
}

func readPlainR(ref string, role protocol.Role, children ...*protocol.ReadNode) *protocol.ReadNode {
	return &protocol.ReadNode{
		Ref: ref, Role: role, Bounds: protocol.HiddenBounds(),
		Label: []string{}, Actions: []protocol.Action{},
		Enabled: readBool(true), Focused: readBool(false), Selected: readBool(false),
		Protected: readBool(false), Checked: readBool(false),
		Children: children,
	}
}

func legacyPackedCompactTree(tree *protocol.Tree) *protocol.ReadTree {
	read := &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact,
		Root: projectLegacyCompactNode(tree.Root, newCompactProjection(tree.Root), nil),
	}
	return PackTree(read)
}

func emptyReadDelta(base string) *protocol.ReadDelta {
	return &protocol.ReadDelta{
		Format: "delta", Base: base, View: protocol.ViewCompact,
	}
}

func TestLegacyCompactChainFullExpandsOnApply(t *testing.T) {
	leaf := nodeT("n4", protocol.RoleText, rectT(10, 10, 200, 100), "tail")
	mid := plainT("n3", protocol.RoleView)
	mid.Children = []*protocol.Node{leaf}
	head := plainT("n2", protocol.RoleView)
	head.Children = []*protocol.Node{mid}

	legacy := legacyPackedCompactTree(compactScreenTree(head))
	if !hasChain(legacy.Root, []string{"n2", "n3"}) {
		t.Fatalf("fixture did not produce a historical chain: %+v", legacy.Root)
	}
	var decoded protocol.ReadTree
	if err := protocolDecode([]byte(readWireText(t, legacy)), &decoded); err != nil {
		t.Fatalf("decode legacy full: %v", err)
	}

	applied, err := ReadApply(&decoded, emptyReadDelta("O1"))
	if err != nil {
		t.Fatalf("ReadApply legacy full: %v", err)
	}
	requireNoReadChains(t, applied.Root)
	expanded := UnpackTree(&decoded)
	if !ReadEqual(applied, expanded) {
		t.Fatal("legacy full replay did not expand to the historical logical tree")
	}
	if readWireText(t, applied) != readWireText(t, expanded) {
		t.Fatal("legacy full replay canonical bytes differ from the expanded tree")
	}
	if childOf(t, applied.Root, "n4") == nil {
		t.Fatal("the chain-tail child n4 is missing")
	}
}

func TestReadDiffExpandsLegacyCurrentChain(t *testing.T) {
	build := func(label string) *protocol.Tree {
		leaf := nodeT("n4", protocol.RoleText, rectT(10, 10, 200, 100), label)
		middle := plainT("n3", protocol.RoleView)
		middle.Children = []*protocol.Node{leaf}
		head := plainT("n2", protocol.RoleView)
		head.Children = []*protocol.Node{middle}
		return compactScreenTree(head)
	}
	base := legacyPackedCompactTree(build("old"))
	current := legacyPackedCompactTree(build("new"))
	if !hasChain(base.Root, []string{"n2", "n3"}) ||
		!hasChain(current.Root, []string{"n2", "n3"}) {
		t.Fatal("fixtures must both contain historical chains")
	}

	delta, err := ReadDiff("O1", base, current)
	if err != nil {
		t.Fatalf("ReadDiff legacy current: %v", err)
	}
	requireNoDeltaChains(t, delta)
	mustReadApplyEqual(t, base, delta, UnpackTree(current))
}

func TestLegacyCompactChainAddDefinitions(t *testing.T) {
	base := readRootR(readR("n2", protocol.RoleText, "existing"))
	tail := readR("n13", protocol.RoleText, "tail")
	inner := readPlainR("n12", protocol.RoleView, tail)
	outer := readPlainR("n11", protocol.RoleView, inner)
	addRoot := readPlainR("n10", protocol.RoleGroup, outer)
	delta := PackDelta(&protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Add:      []*protocol.ReadNode{addRoot},
		Children: map[string][]string{"n1": {"n2", "n10"}},
	})
	if !hasChain(delta.Add[0], []string{"n11", "n12"}) {
		t.Fatalf("fixture did not embed a chain in add: %+v", delta.Add[0])
	}

	decoded := mustReadRoundTrip(t, delta)
	applied, err := ReadApply(base, decoded)
	if err != nil {
		t.Fatalf("ReadApply legacy add: %v", err)
	}
	requireNoReadChains(t, applied.Root)
	requireImmediateRefs(t, applied.Root, "n2", "n10")
	tailRead := childOf(t, applied.Root, "n13")
	if tailRead == nil {
		t.Fatal("the child below the add chain tail is missing")
	}
	parent := childOf(t, applied.Root, "n12")
	if parent == nil || len(parent.Children) != 1 || parent.Children[0].Ref != "n13" {
		t.Fatalf("chain-tail child must belong to n12: %+v", parent)
	}
}

func readLegacyReplaceTree(labelSuffix string) *protocol.ReadTree {
	rows := make([]*protocol.ReadNode, 0, 8)
	for i := 0; i < 8; i++ {
		rows = append(rows, readR(
			fmt.Sprintf("n%d", i+10),
			protocol.RoleText,
			fmt.Sprintf("row-%d-%s", i, labelSuffix),
		))
	}
	tail := readPlainR("n4", protocol.RoleView, rows...)
	inner := readPlainR("n3", protocol.RoleView, tail)
	region := readR("n2", protocol.RoleGroup, "region", inner)
	return readRootR(region)
}

func TestLegacyCompactChainReplaceDefinitions(t *testing.T) {
	legacyBase := PackTree(readLegacyReplaceTree("old"))
	current := UnpackTree(readLegacyReplaceTree("new"))
	if !hasChain(legacyBase.Root, []string{"n3", "n4"}) {
		t.Fatalf("base fixture did not produce a nested chain: %+v", legacyBase.Root)
	}

	delta := mustReadDelta(t, "O1", legacyBase, current)
	if len(delta.Replace) == 0 {
		t.Fatalf("fixture must produce a replace region: %+v", delta)
	}
	packed := PackDelta(delta)
	foundChain := false
	for _, definition := range packed.Replace {
		if hasChain(definition, []string{"n3", "n4"}) {
			foundChain = true
			break
		}
	}
	if !foundChain {
		t.Fatalf("packed replace fixture did not contain a chain: %+v", packed.Replace)
	}
	decoded := mustReadRoundTrip(t, packed)
	mustReadApplyEqual(t, legacyBase, decoded, current)
	applied, err := ReadApply(legacyBase, decoded)
	if err != nil {
		t.Fatalf("ReadApply legacy replace: %v", err)
	}
	requireNoReadChains(t, applied.Root)
}

func TestLegacyCompactChainTailChildReparent(t *testing.T) {
	tailChild := readR("n5", protocol.RoleText, "moving")
	tail := readPlainR("n4", protocol.RoleView, tailChild)
	middle := readPlainR("n3", protocol.RoleView, tail)
	head := readPlainR("n2", protocol.RoleView, middle)
	target := readPlainR("n6", protocol.RoleGroup)
	base := PackTree(readRootR(head, target))
	if !hasChain(base.Root, []string{"n2", "n3", "n4"}) {
		t.Fatalf("fixture did not chain the source tail: %+v", base.Root)
	}

	delta := &protocol.ReadDelta{
		Format: "delta", Base: "O1", View: protocol.ViewCompact,
		Children: map[string][]string{
			"n1": {"n2", "n6"},
			"n4": {},
			"n6": {"n5"},
		},
	}
	decoded := mustReadRoundTrip(t, delta)
	applied, err := ReadApply(base, decoded)
	if err != nil {
		t.Fatalf("reparent below legacy chain: %v", err)
	}
	requireNoReadChains(t, applied.Root)
	moved := childOf(t, applied.Root, "n5")
	targetRead := childOf(t, applied.Root, "n6")
	tailRead := childOf(t, applied.Root, "n4")
	if moved == nil || targetRead == nil || len(targetRead.Children) != 1 ||
		targetRead.Children[0].Ref != "n5" || tailRead == nil || len(tailRead.Children) != 0 {
		t.Fatalf("chain-tail child did not move under its target: %+v", applied.Root)
	}
}
