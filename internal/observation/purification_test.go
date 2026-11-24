package observation

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func defaultEmptyRaw(bounds device.Rect, className string) device.RawNode {
	return device.RawNode{
		Visible:       true,
		RawBounds:     bounds,
		Bounds:        bounds,
		ClassName:     className,
		TextSet:       true,
		Text:          "",
		DescSet:       true,
		Description:   "",
		ResourceSet:   true,
		ResourceID:    "",
		HasEnabled:    true,
		Enabled:       true,
		HasChecked:    true,
		Checked:       false,
		HasSelected:   true,
		Selected:      false,
		HasFocused:    true,
		Focused:       false,
		HasClickable:  true,
		Clickable:     false,
		HasLongClick:  true,
		LongClickable: false,
		HasScrollable: true,
		Scrollable:    false,
		HasCheckable:  true,
		Checkable:     false,
		HasPassword:   true,
		Password:      false,
	}
}

func assertCompactAnonymousPlainAreContainers(t *testing.T, tree *protocol.Tree) {
	t.Helper()
	compact := UnpackTree(ProjectCompactTree(tree))
	if compact == nil || compact.Root == nil {
		t.Fatalf("UnpackTree(ProjectCompactTree) returned nil")
	}
	var walk func(node *protocol.ReadNode)
	walk = func(node *protocol.ReadNode) {
		if node == nil {
			return
		}
		if AnonymousPlain(node) && len(node.Children) == 0 {
			t.Fatalf("anonymous plain node %q must not be published as a leaf", node.Ref)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(compact.Root)
}

func assertSnapshotIndexClosure(t *testing.T, snap *Snapshot, rootIdx int) {
	t.Helper()
	var treeRefs []string
	var walk func(node *protocol.Node)
	walk = func(node *protocol.Node) {
		if node == nil {
			return
		}
		treeRefs = append(treeRefs, node.Ref)
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(snap.Tree.Root)

	if len(snap.Refs) != len(treeRefs) {
		t.Fatalf("Refs %v does not match reachable tree refs %v", snap.Refs, treeRefs)
	}
	seen := make(map[string]bool, len(snap.Refs))
	for idx, wantRef := range treeRefs {
		if seen[wantRef] {
			t.Fatalf("duplicate ref %q in tree preorder %v", wantRef, treeRefs)
		}
		seen[wantRef] = true
		if snap.Refs[idx] != wantRef {
			t.Fatalf("Refs[%d]=%q, want preorder %q (refs=%v tree=%v)", idx, snap.Refs[idx], wantRef, snap.Refs, treeRefs)
		}
		_, inSource := snap.Source[wantRef]
		_, inCtx := snap.ContextSource[wantRef]
		if inSource == inCtx {
			t.Fatalf("ref %q index closure mismatch: inSource=%t inContextSource=%t", wantRef, inSource, inCtx)
		}
	}
	if len(snap.Source)+len(snap.ContextSource) != len(snap.Refs) {
		t.Fatalf("Source(%d) + ContextSource(%d) != Refs(%d)", len(snap.Source), len(snap.ContextSource), len(snap.Refs))
	}
	if gotIdx, ok := snap.Source[snap.Tree.Root.Ref]; !ok || gotIdx != rootIdx {
		t.Fatalf("Source[%q] = %d (%t), want %d", snap.Tree.Root.Ref, gotIdx, ok, rootIdx)
	}
}

func TestEmptyLeafPurification(t *testing.T) {
	for _, emptyCount := range []int{32, 256} {
		t.Run(fmt.Sprintf("empty_leaves_%d", emptyCount), func(t *testing.T) {
			builder := newCap(1080, 2400)
			rootIdx := builder.root()
			for idx := 0; idx < emptyCount; idx++ {
				className := "android.view.View"
				if idx%2 == 1 {
					className = "android.widget.FrameLayout"
				}
				left := (idx % 10) * 100
				top := (idx / 10) * 50
				builder.add(rootIdx, defaultEmptyRaw(rect(left, top, left+90, top+40), className))
			}
			keptRaw := defaultEmptyRaw(rect(10, 2000, 500, 2080), "android.widget.TextView")
			keptRaw.Text = "保留文本"
			keptIdx := builder.add(rootIdx, keptRaw)

			snap := buildOne(t, NewBuilder(), builder.capture())
			compact := ProjectCompactTree(snap.Tree)
			if compact == nil {
				t.Fatalf("ProjectCompactTree returned nil")
			}
			if err := compact.Validate(); err != nil {
				t.Fatalf("compact Validate: %v", err)
			}
			compactBytes, err := protocol.JSON(compact)
			if err != nil {
				t.Fatalf("protocol.JSON(compact): %v", err)
			}

			t.Logf("purification empty_leaves=%d raw_nodes=%d public_nodes=%d root_children=%d compact_full_bytes=%d",
				emptyCount, len(snap.Capture.Nodes), len(snap.Refs), len(snap.Tree.Root.Children), len(compactBytes))

			wantRawNodes := 1 + emptyCount + 1
			if len(snap.Capture.Nodes) != wantRawNodes {
				t.Fatalf("raw nodes = %d, want %d", len(snap.Capture.Nodes), wantRawNodes)
			}
			if len(snap.Refs) != 2 || len(snap.Tree.Root.Children) != 1 {
				t.Fatalf("default empty leaves must be filtered: public_nodes=%d root_children=%d, want 2 and 1",
					len(snap.Refs), len(snap.Tree.Root.Children))
			}
			keptNode := snap.Tree.Root.Children[0]
			if keptNode.Role != protocol.RoleText || len(keptNode.Label) != 1 || keptNode.Label[0] != "保留文本" {
				t.Fatalf("kept sibling = %+v, want text \"保留文本\"", keptNode)
			}
			if gotIdx, ok := snap.Source[keptNode.Ref]; !ok || gotIdx != keptIdx {
				t.Fatalf("kept sibling Source[%q] = %d (%t), want %d", keptNode.Ref, gotIdx, ok, keptIdx)
			}
			assertSnapshotIndexClosure(t, snap, rootIdx)
			assertCompactAnonymousPlainAreContainers(t, snap.Tree)
		})
	}

	t.Run("retention_and_filter_boundaries", func(t *testing.T) {
		builder := newCap(1080, 2400)
		rootIdx := builder.root()

		// Filtered nodes:
		// 1) Resource-only non-interactive leaf.
		resOnly := withRes(defaultEmptyRaw(rect(0, 0, 100, 40), "android.view.View"), "app:id/spacer")
		builder.add(rootIdx, resOnly)
		// 2) All-unknown empty leaf.
		builder.add(rootIdx, withClass(visible(rect(0, 50, 100, 90)), "android.view.View"))
		// 3) Visible empty container whose only child is filtered.
		emptyShell := builder.add(rootIdx, defaultEmptyRaw(rect(0, 100, 300, 200), "android.widget.FrameLayout"))
		emptyNested := builder.add(emptyShell, defaultEmptyRaw(rect(10, 110, 200, 180), "android.view.View"))

		// 4) Default leaves whose only text or description is whitespace.
		wsText := defaultEmptyRaw(rect(0, 210, 100, 250), "android.widget.TextView")
		wsText.Text = "\u00a0"
		builder.add(rootIdx, wsText)
		wsDesc := defaultEmptyRaw(rect(100, 210, 200, 250), "android.view.View")
		wsDesc.Description = "\n "
		builder.add(rootIdx, wsDesc)

		// Retained nodes:
		// 5) Non-empty description.
		descOnly := defaultEmptyRaw(rect(0, 260, 100, 300), "android.view.View")
		descOnly.Description = "说明"
		builder.add(rootIdx, descOnly)
		// 6) Real capabilities: clickable, long-clickable, scrollable, editable, checkable.
		builder.add(rootIdx, clickable(defaultEmptyRaw(rect(0, 310, 100, 350), "android.view.View")))
		builder.add(rootIdx, longClickable(defaultEmptyRaw(rect(0, 360, 100, 400), "android.view.View")))
		builder.add(rootIdx, scrollable(defaultEmptyRaw(rect(0, 410, 100, 500), "android.widget.ScrollView")))
		builder.add(rootIdx, editable(defaultEmptyRaw(rect(0, 510, 100, 550), "android.widget.EditText")))
		checkableOnly := defaultEmptyRaw(rect(0, 560, 100, 600), "android.widget.CheckBox")
		checkableOnly.Checkable = true
		builder.add(rootIdx, checkableOnly)
		// 7) Non-default states: disabled, checked, focused, selected, protected.
		disabled := defaultEmptyRaw(rect(0, 610, 100, 650), "android.view.View")
		disabled.Enabled = false
		builder.add(rootIdx, disabled)
		checked := defaultEmptyRaw(rect(0, 660, 100, 700), "android.view.View")
		checked.Checked = true
		builder.add(rootIdx, checked)
		focusedLeaf := defaultEmptyRaw(rect(0, 710, 100, 750), "android.view.View")
		focusedLeaf.Focused = true
		builder.add(rootIdx, focusedLeaf)
		selectedLeaf := defaultEmptyRaw(rect(0, 760, 100, 800), "android.view.View")
		selectedLeaf.Selected = true
		builder.add(rootIdx, selectedLeaf)
		protectedLeaf := defaultEmptyRaw(rect(0, 810, 100, 850), "android.view.View")
		protectedLeaf.Password = true
		builder.add(rootIdx, protectedLeaf)
		// 8) Unlabelled ImageView intrinsic leaf role.
		builder.add(rootIdx, defaultEmptyRaw(rect(0, 860, 100, 900), "android.widget.ImageView"))
		// 9) Empty visible container holding one retained text child (and a filtered sibling).
		keptShell := builder.add(rootIdx, defaultEmptyRaw(rect(0, 910, 500, 1000), "android.widget.FrameLayout"))
		builder.add(keptShell, defaultEmptyRaw(rect(10, 920, 80, 950), "android.view.View"))
		keptLeaf := defaultEmptyRaw(rect(100, 920, 400, 980), "android.widget.TextView")
		keptLeaf.Text = "容器子项"
		builder.add(keptShell, keptLeaf)

		b := NewBuilder()
		snap := buildOne(t, b, builder.capture())
		const wantKeptTop = 13
		// The only remaining non-top-level nodes are the synthetic root and
		// the kept shell's text. Filtered leaves must not consume refs.
		if want := wantKeptTop + 3; b.nextRef != want {
			t.Fatalf("next ref = %d, want %d", b.nextRef, want)
		}
		if len(snap.Tree.Root.Children) != wantKeptTop {
			t.Fatalf("kept top-level children = %d, want %d\n%s", len(snap.Tree.Root.Children), wantKeptTop, dump(snap.Tree))
		}
		shellNode := snap.Tree.Root.Children[wantKeptTop-1]
		if shellNode.Role != protocol.RoleView || len(shellNode.Children) != 1 || shellNode.Children[0].Label[0] != "容器子项" {
			t.Fatalf("retained container = %+v", shellNode)
		}
		// Raw capture chain must stay intact for downstream revalidation.
		if snap.Capture.Nodes[emptyNested].Parent != emptyShell || snap.Capture.Nodes[emptyShell].Parent != rootIdx {
			t.Fatalf("raw parent chain was modified by purification")
		}
		assertSnapshotIndexClosure(t, snap, rootIdx)
		assertCompactAnonymousPlainAreContainers(t, snap.Tree)
	})
}

func TestCrossDisplayContextAssembly(t *testing.T) {
	t.Run("invisible_ancestor_across_nontarget_wrapper", func(t *testing.T) {
		builder := newCap(1080, 2400)
		rootIdx := builder.root()
		ancestorIdx := builder.add(rootIdx, withClass(device.RawNode{Visible: false}, "android.widget.FrameLayout"))
		wrapperIdx := builder.add(ancestorIdx, withText(withClass(visible(rect(0, 0, 1080, 2400)), "android.widget.FrameLayout"), "非目标包装层"))
		builder.nodes[wrapperIdx].DisplayID = 1
		leafIdx := builder.add(wrapperIdx, withText(withClass(visible(rect(20, 40, 400, 120)), "android.widget.TextView"), "目标文本"))

		b := NewBuilder()
		snap := buildOne(t, b, builder.capture())

		var walkTree func(node *protocol.Node)
		walkTree = func(node *protocol.Node) {
			if node == nil {
				return
			}
			for _, label := range node.Label {
				if label == "非目标包装层" {
					t.Errorf("non-target display wrapper must not be published: %+v", node)
				}
			}
			for _, child := range node.Children {
				walkTree(child)
			}
		}
		walkTree(snap.Tree.Root)

		for ref, rawIdx := range snap.Source {
			if rawIdx == wrapperIdx {
				t.Errorf("non-target display wrapper leaked into Source[%q]=%d", ref, rawIdx)
			}
		}
		for ref, rawIdx := range snap.ContextSource {
			if rawIdx == wrapperIdx {
				t.Errorf("non-target display wrapper leaked into ContextSource[%q]=%d", ref, rawIdx)
			}
		}

		if len(snap.Tree.Root.Children) != 1 {
			t.Fatalf("root children = %d, want 1 context ancestor (refs=%v)", len(snap.Tree.Root.Children), snap.Refs)
		}
		ctxNode := snap.Tree.Root.Children[0]
		if !ctxNode.Context || ctxNode.Role != protocol.RoleGroup || ctxNode.Bounds != nil {
			t.Fatalf("target invisible ancestor must become group context without bounds: %+v", ctxNode)
		}
		if ctxNode.Enabled != nil || ctxNode.Focused != nil || ctxNode.Selected != nil ||
			ctxNode.Protected != nil || ctxNode.Checked != nil {
			t.Fatalf("context node must keep all five tri-state flags nil: %+v", ctxNode)
		}
		if len(ctxNode.Children) != 1 {
			t.Fatalf("context children = %d, want 1 kept target text leaf", len(ctxNode.Children))
		}
		leafNode := ctxNode.Children[0]
		if leafNode.Context || leafNode.Role != protocol.RoleText || len(leafNode.Label) != 1 || leafNode.Label[0] != "目标文本" {
			t.Fatalf("leaf under context = %+v, want text \"目标文本\"", leafNode)
		}

		assertSnapshotIndexClosure(t, snap, rootIdx)
		if gotIdx, ok := snap.Source[leafNode.Ref]; !ok || gotIdx != leafIdx {
			t.Fatalf("Source[%q] = %d (%t), want %d", leafNode.Ref, gotIdx, ok, leafIdx)
		}
		if gotIdx, ok := snap.ContextSource[ctxNode.Ref]; !ok || gotIdx != ancestorIdx {
			t.Fatalf("ContextSource[%q] = %d (%t), want %d", ctxNode.Ref, gotIdx, ok, ancestorIdx)
		}

		// Next identical capture must preserve context and descendant refs via ContextSource.
		next := buildOne(t, b, builder.capture())
		if next.Tree.Root.Children[0].Ref != ctxNode.Ref ||
			next.Tree.Root.Children[0].Children[0].Ref != leafNode.Ref {
			t.Fatalf("context continuity broke across wrapper: got %v, want [%s %s %s]",
				next.Refs, snap.Tree.Root.Ref, ctxNode.Ref, leafNode.Ref)
		}
	})

	t.Run("direct_root_wrapper_and_sibling_preorder", func(t *testing.T) {
		builder := newCap(1080, 2400)
		rootIdx := builder.root()
		emptyWrapper := builder.add(rootIdx, withText(withClass(visible(rect(0, 0, 100, 100)), "android.widget.FrameLayout"), "纯非目标"))
		builder.nodes[emptyWrapper].DisplayID = 1

		wrapper := builder.add(rootIdx, withClass(visible(rect(0, 0, 1080, 1000)), "android.widget.FrameLayout"))
		builder.nodes[wrapper].DisplayID = 1
		firstIdx := builder.add(wrapper, withText(withClass(visible(rect(0, 10, 300, 80)), "android.widget.TextView"), "甲"))
		secondIdx := builder.add(wrapper, withText(withClass(visible(rect(0, 90, 300, 160)), "android.widget.TextView"), "乙"))

		thirdIdx := builder.add(rootIdx, withText(withClass(visible(rect(0, 200, 300, 280)), "android.widget.TextView"), "丙"))

		snap := buildOne(t, NewBuilder(), builder.capture())
		if len(snap.Tree.Root.Children) != 3 {
			t.Fatalf("root children = %d, want 3 target leaves in preorder\n%s", len(snap.Tree.Root.Children), dump(snap.Tree))
		}
		wantLabels := []string{"甲", "乙", "丙"}
		wantIndices := []int{firstIdx, secondIdx, thirdIdx}
		for idx, child := range snap.Tree.Root.Children {
			if len(child.Label) != 1 || child.Label[0] != wantLabels[idx] {
				t.Fatalf("child[%d] label = %v, want %q", idx, child.Label, wantLabels[idx])
			}
			if gotIdx := snap.Source[child.Ref]; gotIdx != wantIndices[idx] {
				t.Fatalf("child[%d] Source[%q] = %d, want %d", idx, child.Ref, gotIdx, wantIndices[idx])
			}
		}
		assertSnapshotIndexClosure(t, snap, rootIdx)
	})

	t.Run("visible_empty_container_across_nontarget_wrapper", func(t *testing.T) {
		builder := newCap(1080, 2400)
		rootIdx := builder.root()
		containerIdx := builder.add(rootIdx, defaultEmptyRaw(rect(0, 0, 1080, 1200), "android.widget.FrameLayout"))
		wrapperIdx := builder.add(containerIdx, withText(withClass(visible(rect(0, 0, 1080, 1200)), "android.widget.FrameLayout"), "跨屏壳"))
		builder.nodes[wrapperIdx].DisplayID = 1
		leafIdx := builder.add(wrapperIdx, withText(withClass(visible(rect(10, 10, 300, 90)), "android.widget.TextView"), "可见后代"))

		snap := buildOne(t, NewBuilder(), builder.capture())
		if len(snap.Tree.Root.Children) != 1 {
			t.Fatalf("root children = %d, want 1 visible container", len(snap.Tree.Root.Children))
		}
		containerNode := snap.Tree.Root.Children[0]
		if containerNode.Context || containerNode.Role != protocol.RoleView {
			t.Fatalf("visible target container = %+v, want non-context view", containerNode)
		}
		if len(containerNode.Children) != 1 || containerNode.Children[0].Label[0] != "可见后代" {
			t.Fatalf("container children = %+v, want single target text leaf", containerNode.Children)
		}
		if gotIdx := snap.Source[containerNode.Ref]; gotIdx != containerIdx {
			t.Fatalf("container Source[%q] = %d, want %d", containerNode.Ref, gotIdx, containerIdx)
		}
		if gotIdx := snap.Source[containerNode.Children[0].Ref]; gotIdx != leafIdx {
			t.Fatalf("leaf Source[%q] = %d, want %d", containerNode.Children[0].Ref, gotIdx, leafIdx)
		}
		assertSnapshotIndexClosure(t, snap, rootIdx)
	})
}

func stateTestNode(focused, checked *bool) *protocol.Node {
	return &protocol.Node{
		Ref:       "n2",
		Role:      protocol.RoleText,
		Bounds:    &protocol.Bounds{Left: 0, Top: 0, Right: 100, Bottom: 50},
		Label:     []string{"状态"},
		Actions:   []protocol.Action{},
		Enabled:   boolp(true),
		Focused:   focused,
		Selected:  boolp(false),
		Protected: boolp(false),
		Checked:   checked,
		Children:  []*protocol.Node{},
	}
}

func stateTestTree(child *protocol.Node) *protocol.Tree {
	return &protocol.Tree{
		Format: "full",
		Scope:  protocol.Scope,
		Root: &protocol.Node{
			Ref:       "n1",
			Role:      protocol.RoleRoot,
			Bounds:    &protocol.Bounds{Left: 0, Top: 0, Right: 1080, Bottom: 2400},
			Label:     []string{},
			Actions:   []protocol.Action{},
			Enabled:   boolp(true),
			Focused:   boolp(false),
			Selected:  boolp(false),
			Protected: boolp(false),
			Checked:   boolp(false),
			Children:  []*protocol.Node{child},
		},
	}
}

func TestCheckedWireAndStrictTriBool(t *testing.T) {
	t.Run("node_checked_false_omitted", func(t *testing.T) {
		node := stateTestNode(boolp(false), boolp(false))
		raw, err := protocol.JSON(node)
		if err != nil {
			t.Fatalf("protocol.JSON: %v", err)
		}
		if strings.Contains(string(raw), `"checked"`) {
			t.Fatalf("protocol.Node Checked=false must omit checked field, got: %s", raw)
		}
		var decoded protocol.Node
		if err := protocol.Decode(raw, &decoded); err != nil {
			t.Fatalf("protocol.Decode: %v", err)
		}
		if decoded.Checked == nil || *decoded.Checked {
			t.Fatalf("absent checked in complete Node must decode to known false, got: %v", decoded.Checked)
		}
	})

	t.Run("node_checked_nil_explicit_null", func(t *testing.T) {
		node := stateTestNode(boolp(false), nil)
		raw, err := protocol.JSON(node)
		if err != nil {
			t.Fatalf("protocol.JSON: %v", err)
		}
		if !strings.Contains(string(raw), `"checked":null`) {
			t.Fatalf("protocol.Node Checked=nil must encode \"checked\":null, got: %s", raw)
		}
		var decoded protocol.Node
		if err := protocol.Decode(raw, &decoded); err != nil {
			t.Fatalf("protocol.Decode: %v", err)
		}
		if decoded.Checked != nil {
			t.Fatalf("explicit checked:null must decode to nil, got: %t", *decoded.Checked)
		}
	})

	t.Run("node_checked_true_explicit", func(t *testing.T) {
		node := stateTestNode(boolp(false), boolp(true))
		raw, err := protocol.JSON(node)
		if err != nil {
			t.Fatalf("protocol.JSON: %v", err)
		}
		if !strings.Contains(string(raw), `"checked":true`) {
			t.Fatalf("protocol.Node Checked=true must encode \"checked\":true, got: %s", raw)
		}
		var decoded protocol.Node
		if err := protocol.Decode(raw, &decoded); err != nil {
			t.Fatalf("protocol.Decode: %v", err)
		}
		if decoded.Checked == nil || !*decoded.Checked {
			t.Fatalf("checked:true must decode to known true, got: %v", decoded.Checked)
		}
	})

	t.Run("delta_add_replace_and_detailed_checked_matrix", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			checked     *bool
			wantSnippet string
			wantAbsent  bool
		}{
			{"false_omitted", boolp(false), `"checked"`, true},
			{"nil_null", nil, `"checked":null`, false},
			{"true_explicit", boolp(true), `"checked":true`, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				node := stateTestNode(boolp(false), tc.checked)
				detailedWire, err := protocol.JSON(ProjectDetailedNode(node))
				if err != nil {
					t.Fatalf("protocol.JSON(detailed): %v", err)
				}
				if tc.wantAbsent == strings.Contains(string(detailedWire), tc.wantSnippet) {
					t.Fatalf("detailed wire %s check failed: %s", tc.name, detailedWire)
				}

				addChild := stateTestNode(boolp(false), tc.checked)
				addChild.Ref = "n3"
				replaceNode := stateTestNode(boolp(false), tc.checked)
				delta := &protocol.Delta{
					Format:   "delta",
					Base:     "O1",
					Add:      []*protocol.Node{addChild},
					Children: map[string][]string{"n1": {"n2", "n3"}},
					Replace:  map[string]*protocol.Node{"n2": replaceNode},
				}
				deltaWire, err := protocol.JSON(delta)
				if err != nil {
					t.Fatalf("protocol.JSON(delta): %v", err)
				}
				if tc.wantAbsent == strings.Contains(string(deltaWire), tc.wantSnippet) {
					t.Fatalf("delta add/replace wire %s check failed: %s", tc.name, deltaWire)
				}
				var decoded protocol.Delta
				if err := protocol.Decode(deltaWire, &decoded); err != nil {
					t.Fatalf("protocol.Decode(delta): %v", err)
				}
				gotAdd := decoded.Add[0].Checked
				gotRep := decoded.Replace["n2"].Checked
				if tc.checked == nil {
					if gotAdd != nil || gotRep != nil {
						t.Fatalf("decoded nil checked became non-nil: add=%v replace=%v", gotAdd, gotRep)
					}
				} else if gotAdd == nil || *gotAdd != *tc.checked || gotRep == nil || *gotRep != *tc.checked {
					t.Fatalf("decoded checked mismatch: add=%v replace=%v want %t", gotAdd, gotRep, *tc.checked)
				}
			})
		}
	})

	t.Run("synthetic_root_and_context_wire", func(t *testing.T) {
		builder := newCap(1080, 2400)
		rootIdx := builder.root()
		ctxIdx := builder.add(rootIdx, withClass(device.RawNode{Visible: false}, "android.widget.FrameLayout"))
		builder.add(ctxIdx, withText(withClass(visible(rect(0, 0, 200, 80)), "android.widget.TextView"), "文本"))

		snap := buildOne(t, NewBuilder(), builder.capture())
		if snap.Tree.Root.Checked == nil || *snap.Tree.Root.Checked {
			t.Fatalf("synthetic root Checked = %v, want known false", snap.Tree.Root.Checked)
		}
		rootWire, err := protocol.JSON(snap.Tree.Root)
		if err != nil {
			t.Fatalf("protocol.JSON(root): %v", err)
		}
		rootHeader := strings.Split(string(rootWire), `"children"`)[0]
		if strings.Contains(rootHeader, `"checked"`) {
			t.Fatalf("synthetic root with known false checked must omit checked in wire: %s", rootHeader)
		}

		ctxNode := snap.Tree.Root.Children[0]
		ctxWire, err := protocol.JSON(ProjectDetailedNode(ctxNode))
		if err != nil {
			t.Fatalf("protocol.JSON(context): %v", err)
		}
		for _, want := range []string{`"enabled":null`, `"focused":null`, `"selected":null`, `"protected":null`, `"checked":null`} {
			if !strings.Contains(string(ctxWire), want) {
				t.Fatalf("context node wire missing %s: %s", want, ctxWire)
			}
		}
		leafWire, err := protocol.JSON(ProjectDetailedNode(ctxNode.Children[0]))
		if err != nil {
			t.Fatalf("protocol.JSON(leaf): %v", err)
		}
		if !strings.Contains(string(leafWire), `"checked":null`) {
			t.Fatalf("leaf without reported checked must keep explicit null: %s", leafWire)
		}
	})

	t.Run("strict_tri_bool_transitions_all_five_flags", func(t *testing.T) {
		fields := []struct {
			name   string
			wire   string
			assign func(n *protocol.Node, v *bool)
			read   func(n *protocol.ReadNode) *bool
		}{
			{
				name:   "enabled",
				wire:   `"enabled"`,
				assign: func(n *protocol.Node, v *bool) { n.Enabled = v },
				read:   func(n *protocol.ReadNode) *bool { return n.Enabled },
			},
			{
				name:   "focused",
				wire:   `"focused"`,
				assign: func(n *protocol.Node, v *bool) { n.Focused = v },
				read:   func(n *protocol.ReadNode) *bool { return n.Focused },
			},
			{
				name:   "selected",
				wire:   `"selected"`,
				assign: func(n *protocol.Node, v *bool) { n.Selected = v },
				read:   func(n *protocol.ReadNode) *bool { return n.Selected },
			},
			{
				name:   "protected",
				wire:   `"protected"`,
				assign: func(n *protocol.Node, v *bool) { n.Protected = v },
				read:   func(n *protocol.ReadNode) *bool { return n.Protected },
			},
			{
				name:   "checked",
				wire:   `"checked"`,
				assign: func(n *protocol.Node, v *bool) { n.Checked = v },
				read:   func(n *protocol.ReadNode) *bool { return n.Checked },
			},
		}
		states := []struct {
			label string
			val   *bool
			json  string
		}{
			{"nil", nil, "null"},
			{"false", boolp(false), "false"},
			{"true", boolp(true), "true"},
		}

		for _, field := range fields {
			for _, from := range states {
				for _, to := range states {
					name := fmt.Sprintf("%s_%s_to_%s", field.name, from.label, to.label)
					t.Run(name, func(t *testing.T) {
						nBase := stateTestNode(boolp(false), boolp(false))
						nCur := stateTestNode(boolp(false), boolp(false))
						field.assign(nBase, from.val)
						field.assign(nCur, to.val)
						base := stateTestTree(nBase)
						cur := stateTestTree(nCur)
						readBase := ProjectCompactTree(base)
						readCur := ProjectCompactTree(cur)

						same := from.label == to.label
						if Equal(base, cur) != same {
							t.Fatalf("Equal(%s->%s) = %t, want %t", from.label, to.label, !same, same)
						}
						if ReadEqual(readBase, readCur) != same {
							t.Fatalf("ReadEqual(%s->%s) = %t, want %t", from.label, to.label, !same, same)
						}

						delta, err := Diff("O1", base, cur)
						if err != nil {
							t.Fatalf("Diff: %v", err)
						}
						readDelta, err := ReadDiff("O1", readBase, readCur)
						if err != nil {
							t.Fatalf("ReadDiff: %v", err)
						}
						if delta.IsEmpty() != same || readDelta.IsEmpty() != same {
							t.Fatalf("empty mismatch: delta=%t readDelta=%t want %t", delta.IsEmpty(), readDelta.IsEmpty(), same)
						}
						if !same {
							wantSnippet := field.wire + ":" + to.json
							deltaJSON, err := protocol.JSON(delta)
							if err != nil {
								t.Fatalf("protocol.JSON(delta): %v", err)
							}
							if !strings.Contains(string(deltaJSON), wantSnippet) {
								t.Fatalf("delta JSON missing %s: %s", wantSnippet, deltaJSON)
							}
							readDeltaJSON, err := protocol.JSON(readDelta)
							if err != nil {
								t.Fatalf("protocol.JSON(readDelta): %v", err)
							}
							if !strings.Contains(string(readDeltaJSON), wantSnippet) {
								t.Fatalf("readDelta JSON missing %s: %s", wantSnippet, readDeltaJSON)
							}
							if strings.Contains(string(readDeltaJSON), `"defaults"`) {
								t.Fatalf("readDelta JSON must not contain defaults: %s", readDeltaJSON)
							}
						}

						applied, err := Apply(base, delta)
						if err != nil {
							t.Fatalf("Apply: %v", err)
						}
						if !Equal(applied, cur) {
							t.Fatalf("Apply mismatch")
						}
						readApplied, err := ReadApply(readBase, readDelta)
						if err != nil {
							t.Fatalf("ReadApply: %v", err)
						}
						if !ReadEqual(readApplied, readCur) {
							t.Fatalf("ReadApply mismatch")
						}
						gotBytes, err := protocol.JSON(readApplied)
						if err != nil {
							t.Fatalf("protocol.JSON(readApplied): %v", err)
						}
						wantBytes, err := protocol.JSON(readCur)
						if err != nil {
							t.Fatalf("protocol.JSON(readCur): %v", err)
						}
						if !bytes.Equal(gotBytes, wantBytes) {
							t.Fatalf("canonical JSON mismatch:\ngot:  %s\nwant: %s", gotBytes, wantBytes)
						}
						_ = field.read(readApplied.Root.Children[0])
					})
				}
			}
		}
	})
}
