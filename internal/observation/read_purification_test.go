package observation

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The compact purification tests cover the five bottom-up rules of the compact
// projection in their fixed order: unlabeled non-interactive image leaves, passive
// text leaves already carried by an ancestor composite label, anonymous containers
// left empty by those removals, labeled-clickable view hoisting, and source
// resource shortening. They also cover the two downward rules: an input label that
// repeats its value and viewport-edge sliver views.

func plainViewT(ref string) *protocol.Node { return plainT(ref, protocol.RoleView) }

func TestCompactPurificationCompositeLabelCoverage(t *testing.T) {
	composite := actionNodeT("n2", protocol.RoleButton, rectT(0, 0, 400, 200), protocol.ActionTap)
	composite.Label = []string{"创作者小助手, 16:48 发布, 68"}

	covered := []string{"创作者小助手", "68", "发布", "16:48 发布", "68 ", "创作者"}
	coveredChildren := make([]*protocol.Node, 0, len(covered))
	for i, label := range covered {
		coveredChildren = append(coveredChildren,
			nodeT("c"+itoa(i+1), protocol.RoleText, rectT(0, i*10, 200, i*10+10), label))
	}
	// A leaf whose two parts are each carried by the ancestor is covered too.
	twoParts := nodeT("c20", protocol.RoleText, rectT(0, 100, 200, 120))
	twoParts.Label = []string{"创作者小助手", "发布"}
	coveredChildren = append(coveredChildren, twoParts)

	kept := []string{"48", "8", "16:4"}
	keptChildren := make([]*protocol.Node, 0, len(kept)+1)
	for i, label := range kept {
		keptChildren = append(keptChildren,
			nodeT("k"+itoa(i+1), protocol.RoleText, rectT(0, 200+i*10, 200, 210+i*10), label))
	}
	// A non-default state is never a passive text leaf.
	selected := nodeT("k20", protocol.RoleText, rectT(0, 300, 200, 320), "68")
	selected.Selected = readBool(true)
	keptChildren = append(keptChildren, selected)

	composite.Children = append(coveredChildren, keptChildren...)
	compact := ProjectCompactTree(rootT(composite))
	if node := childOf(t, compact.Root, "n2"); node == nil {
		t.Fatal("the composite button must remain")
	}
	for i := range covered {
		if node := childOf(t, compact.Root, "c"+itoa(i+1)); node != nil {
			t.Fatalf("covered text leaf %q must be pruned: %+v", covered[i], node)
		}
	}
	if node := childOf(t, compact.Root, "c20"); node != nil {
		t.Fatalf("the two-part covered leaf must be pruned: %+v", node)
	}
	for i := range kept {
		if node := childOf(t, compact.Root, "k"+itoa(i+1)); node == nil {
			t.Fatalf("independent text leaf %q must be kept", kept[i])
		}
	}
	if node := childOf(t, compact.Root, "k20"); node == nil {
		t.Fatal("a non-default text leaf must be kept")
	}

	// Without any ancestor label nothing is covered.
	orphan := nodeT("n3", protocol.RoleText, rectT(0, 400, 200, 420), "创作者小助手")
	bare := ProjectCompactTree(rootT(orphan))
	if childOf(t, bare.Root, "n3") == nil {
		t.Fatal("a text leaf without a composite ancestor must be kept")
	}
}

func TestCompactPurificationHoistsTextChildren(t *testing.T) {
	t.Run("image and text under an anonymous wrapper", func(t *testing.T) {
		shell := actionNodeT("n2", protocol.RoleView, rectT(0, 0, 300, 100), protocol.ActionTap)
		shell.Resource = readString("app:id/card")
		wrapper := plainViewT("n3")
		wrapper.Children = []*protocol.Node{
			nodeT("n4", protocol.RoleImage, rectT(0, 0, 50, 50)),
			nodeT("n5", protocol.RoleText, rectT(50, 0, 300, 40), "歌手"),
		}
		shell.Children = []*protocol.Node{wrapper}

		compact := ProjectCompactTree(rootT(shell))
		node := childOf(t, compact.Root, "n2")
		if node == nil {
			t.Fatal("the hoisted shell is missing")
		}
		if node.Role != protocol.RoleButton {
			t.Fatalf("shell role = %s, want button", node.Role)
		}
		if len(node.Label) != 1 || node.Label[0] != "歌手" {
			t.Fatalf("hoisted label = %v, want [歌手]", node.Label)
		}
		if len(node.Children) != 0 {
			t.Fatalf("hoisted children = %d, want none", len(node.Children))
		}
		if node.Resource != nil {
			t.Fatalf("a hoisted button must drop its resource hint: %v", *node.Resource)
		}
		if node.Bounds.Kind != protocol.GeometryPoint {
			t.Fatalf("hoisted button geometry = %v, want center point", node.Bounds.Kind)
		}
	})

	t.Run("two text children keep separate label parts", func(t *testing.T) {
		shell := actionNodeT("n2", protocol.RoleView, rectT(0, 200, 300, 300), protocol.ActionTap)
		shell.Children = []*protocol.Node{
			nodeT("n3", protocol.RoleText, rectT(0, 200, 150, 230), "¥99"),
			nodeT("n4", protocol.RoleText, rectT(150, 200, 300, 230), "包邮"),
		}
		compact := ProjectCompactTree(rootT(shell))
		node := childOf(t, compact.Root, "n2")
		if node == nil || node.Role != protocol.RoleButton {
			t.Fatalf("two-text shell = %+v, want button", node)
		}
		if len(node.Label) != 2 || node.Label[0] != "¥99" || node.Label[1] != "包邮" {
			t.Fatalf("hoisted label parts = %v, want [¥99 包邮]", node.Label)
		}
	})

	t.Run("never hoists interactive or multi-branch children", func(t *testing.T) {
		// A card holding an inner button keeps both rows.
		card := actionNodeT("n2", protocol.RoleView, rectT(0, 0, 300, 200), protocol.ActionTap)
		inner := actionNodeT("n3", protocol.RoleButton, rectT(40, 40, 260, 120), protocol.ActionTap)
		inner.Label = []string{"inner"}
		card.Children = []*protocol.Node{inner}

		// A multi-child anonymous container is never flattened.
		container := plainViewT("n10")
		container.Children = []*protocol.Node{
			nodeT("n11", protocol.RoleText, rectT(0, 300, 100, 330), "a"),
			nodeT("n12", protocol.RoleText, rectT(0, 340, 100, 370), "b"),
		}

		// A labelled view is not an unlabeled shell.
		labelled := actionNodeT("n20", protocol.RoleView, rectT(0, 400, 300, 500), protocol.ActionTap)
		labelled.Label = []string{"card"}
		labelled.Children = []*protocol.Node{
			nodeT("n21", protocol.RoleText, rectT(0, 400, 200, 430), "正文"),
		}

		// A shell with a swipe action is not a tap shell.
		swipe := actionNodeT("n30", protocol.RoleView, rectT(0, 600, 300, 700), protocol.ActionSwipe)
		swipe.Children = []*protocol.Node{
			nodeT("n31", protocol.RoleText, rectT(0, 600, 200, 630), "滑动"),
		}

		compact := ProjectCompactTree(rootT(card, container, labelled, swipe))
		if node := childOf(t, compact.Root, "n2"); node == nil || node.Role != protocol.RoleView ||
			len(node.Children) != 1 {
			t.Fatalf("card with an inner button must stay a view: %+v", node)
		}
		containerRead := childOf(t, compact.Root, "n10")
		if containerRead == nil || len(containerRead.Children) != 2 {
			t.Fatalf("multi-child anonymous container must not be flattened: %+v", containerRead)
		}
		if node := childOf(t, compact.Root, "n20"); node == nil || node.Role != protocol.RoleView ||
			len(node.Children) != 1 {
			t.Fatalf("a labelled view must not be hoisted: %+v", node)
		}
		if node := childOf(t, compact.Root, "n30"); node == nil || node.Role != protocol.RoleView {
			t.Fatalf("a swipe shell must not be hoisted: %+v", node)
		}
	})
}

func TestCompactPurificationInputLabelDedup(t *testing.T) {
	duplicate := plainT("n2", protocol.RoleInput)
	duplicate.Label = []string{"hello"}
	duplicate.Value = readString("hello")
	duplicate.Actions = []protocol.Action{protocol.ActionText}

	different := plainT("n3", protocol.RoleInput)
	different.Label = []string{"hint"}
	different.Value = readString("hello")
	different.Actions = []protocol.Action{protocol.ActionText}

	twoParts := plainT("n4", protocol.RoleInput)
	twoParts.Label = []string{"hello", "hint"}
	twoParts.Value = readString("hello")
	twoParts.Actions = []protocol.Action{protocol.ActionText}

	compact := ProjectCompactTree(rootT(duplicate, different, twoParts))
	node := childOf(t, compact.Root, "n2")
	if node == nil || len(node.Label) != 0 {
		t.Fatalf("an input label equal to its value must be dropped: %+v", node)
	}
	if node.Value == nil || *node.Value != "hello" {
		t.Fatalf("the input value must keep the text: %+v", node.Value)
	}
	if node := childOf(t, compact.Root, "n3"); node == nil || len(node.Label) != 1 ||
		node.Label[0] != "hint" {
		t.Fatalf("a distinct input label must remain: %+v", node)
	}
	if node := childOf(t, compact.Root, "n4"); node == nil || len(node.Label) != 2 {
		t.Fatalf("a multi-part input label must remain: %+v", node)
	}
}

func TestCompactPurificationSliverViews(t *testing.T) {
	cases := []struct {
		name      string
		node      *protocol.Node
		wantPrune bool
	}{
		{
			name:      "two_pixel_tappable_view",
			node:      actionNodeT("n2", protocol.RoleView, rectT(0, 0, 200, 2), protocol.ActionTap),
			wantPrune: true,
		},
		{
			name:      "four_pixel_tappable_view",
			node:      actionNodeT("n2", protocol.RoleView, rectT(0, 0, 200, 4), protocol.ActionTap),
			wantPrune: true,
		},
		{
			name:      "five_pixel_tappable_view",
			node:      actionNodeT("n2", protocol.RoleView, rectT(0, 0, 200, 5), protocol.ActionTap),
			wantPrune: false,
		},
		{
			name:      "two_pixel_unlabeled_view",
			node:      nodeT("n2", protocol.RoleView, rectT(0, 0, 200, 2)),
			wantPrune: true,
		},
		{
			name:      "two_pixel_labelled_view",
			node:      nodeT("n2", protocol.RoleView, rectT(0, 0, 200, 2), "提示"),
			wantPrune: false,
		},
		{
			name:      "two_pixel_image",
			node:      actionNodeT("n2", protocol.RoleImage, rectT(0, 0, 200, 2), protocol.ActionTap),
			wantPrune: false,
		},
		{
			name:      "two_pixel_input",
			node:      actionNodeT("n2", protocol.RoleInput, rectT(0, 0, 200, 2), protocol.ActionText),
			wantPrune: false,
		},
		{
			name:      "two_pixel_swipe_view",
			node:      actionNodeT("n2", protocol.RoleView, rectT(0, 0, 200, 2), protocol.ActionSwipe),
			wantPrune: false,
		},
		{
			name:      "clipped_four_pixel_view",
			node:      actionNodeT("n2", protocol.RoleView, rectT(-100, 0, 4, 100), protocol.ActionTap),
			wantPrune: true,
		},
		{
			name:      "clipped_five_pixel_view",
			node:      actionNodeT("n2", protocol.RoleView, rectT(-100, 0, 5, 100), protocol.ActionTap),
			wantPrune: false,
		},
		{
			name:      "fully_offscreen_view",
			node:      actionNodeT("n2", protocol.RoleView, rectT(2000, 2000, 2100, 2100), protocol.ActionTap),
			wantPrune: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selected := actionNodeT("n3", protocol.RoleView, rectT(0, 300, 200, 400), protocol.ActionTap)
			selected.Selected = readBool(true)
			tree := rootT(tc.node, selected)
			compact := ProjectCompactTree(tree)
			got := childOf(t, compact.Root, "n2")
			if tc.wantPrune && got != nil {
				t.Fatalf("sliver view must be pruned: %+v", got)
			}
			if !tc.wantPrune && got == nil {
				t.Fatal("view must be retained")
			}
			// The sibling keeps the screen denominator and the projection total.
			if childOf(t, compact.Root, "n3") == nil {
				t.Fatal("the control sibling must be retained")
			}
		})
	}
}

func TestCompactPurificationSliverWithoutScreen(t *testing.T) {
	thin := actionNodeT("n2", protocol.RoleView, rectT(0, 0, 200, 2), protocol.ActionTap)
	wide := actionNodeT("n3", protocol.RoleView, rectT(0, 0, 200, 5), protocol.ActionTap)
	tree := rootT(thin, wide)
	tree.Root.Bounds = nil
	compact := ProjectCompactTree(tree)
	if childOf(t, compact.Root, "n2") != nil {
		t.Fatal("a two-pixel view must be pruned without a screen rectangle too")
	}
	if childOf(t, compact.Root, "n3") == nil {
		t.Fatal("a five-pixel view must survive without a screen rectangle")
	}
}

func TestCompactPurificationResourceTail(t *testing.T) {
	cases := map[string]string{
		"Answer:Card:OpenUrl:content_card:5:negativeFeedback": "negativeFeedback",
		"Answer:Card:OpenUrl:content_card:5":                  "content_card",
		"app:id/content_card:5":                               "content_card",
		"app/id/section/cover":                                "cover",
		"1:2:3":                                               "3",
		"single":                                              "single",
	}
	for resource, want := range cases {
		shell := actionNodeT("n2", protocol.RoleView, rectT(0, 0, 300, 100), protocol.ActionTap)
		shell.Resource = readString(resource)
		compact := ProjectCompactTree(rootT(shell))
		node := childOf(t, compact.Root, "n2")
		if node == nil || node.Resource == nil {
			t.Fatalf("%q: the resource hint is missing: %+v", resource, node)
		}
		if *node.Resource != want {
			t.Fatalf("resource %q tail = %q, want %q", resource, *node.Resource, want)
		}
	}

	// An empty resource hint is never shortened or invented.
	empty := actionNodeT("n2", protocol.RoleView, rectT(0, 0, 300, 100), protocol.ActionTap)
	empty.Resource = readString("")
	node := childOf(t, ProjectCompactTree(rootT(empty)).Root, "n2")
	if node == nil || node.Resource == nil || *node.Resource != "" {
		t.Fatalf("empty resource hint must stay empty: %+v", node)
	}
}

func TestCompactPointGeometrySuppressesMicroResizeNoise(t *testing.T) {
	// A symmetric one-pixel growth keeps the center, so the point geometry is
	// unchanged and the delta stays empty.
	before := actionNodeT("n2", protocol.RoleButton, rectT(0, 0, 100, 100), protocol.ActionTap)
	after := actionNodeT("n2", protocol.RoleButton, rectT(0, 0, 101, 100), protocol.ActionTap)
	base := ProjectCompactTree(rootT(before))
	current := ProjectCompactTree(rootT(after))
	delta := mustReadDelta(t, "O1", base, current)
	if !delta.IsEmpty() {
		t.Fatalf("an unchanged center point must not produce delta noise: %+v", delta)
	}

	// A real move publishes the new center point.
	moved := actionNodeT("n2", protocol.RoleButton, rectT(0, 0, 102, 100), protocol.ActionTap)
	movedTree := ProjectCompactTree(rootT(moved))
	movedDelta := mustReadDelta(t, "O1", base, movedTree)
	set, ok := movedDelta.Set["n2"]
	if !ok || !set.Bounds.Set || set.Bounds.Value.Point != (protocol.Point{X: 51, Y: 50}) {
		t.Fatalf("moved center point = %+v, want (51,50)", set.Bounds)
	}
	if !strings.Contains(readWireText(t, movedDelta), `"bounds":[51,50]`) {
		t.Fatal("the delta wire must encode the binary point")
	}
	mustReadApplyEqual(t, base, movedDelta, movedTree)
}

func TestCompactPurificationDeltaRoleFlipIsNotRepresentable(t *testing.T) {
	// A shell that carries a text child is hoisted to a button; once it loses the
	// text child it stays a view under the same ref, so no delta can represent it.
	base := ProjectCompactTree(rootT(func() *protocol.Node {
		shell := actionNodeT("n2", protocol.RoleView, rectT(0, 0, 300, 100), protocol.ActionTap)
		shell.Children = []*protocol.Node{
			nodeT("n3", protocol.RoleText, rectT(0, 0, 200, 30), "标题"),
		}
		return shell
	}()))
	current := ProjectCompactTree(rootT(
		actionNodeT("n2", protocol.RoleView, rectT(0, 0, 300, 100), protocol.ActionTap),
	))
	if childOf(t, base.Root, "n2").Role != protocol.RoleButton {
		t.Fatalf("baseline shell must be a hoisted button: %+v", childOf(t, base.Root, "n2"))
	}
	if childOf(t, current.Root, "n2").Role != protocol.RoleView {
		t.Fatalf("current shell must be a view: %+v", childOf(t, current.Root, "n2"))
	}
	if _, err := ReadDiff("O1", base, current); err == nil {
		t.Fatal("a same-ref role flip must not be representable as a delta")
	}
}

// readNodeAt returns the compact node that maps to one raw capture index, or nil
// when the compact view removed it.
func readNodeAt(t *testing.T, compact *protocol.ReadTree, snap *Snapshot, rawIdx int) *protocol.ReadNode {
	t.Helper()
	for ref, index := range snap.Source {
		if index == rawIdx {
			return childOf(t, compact.Root, ref)
		}
	}
	t.Fatalf("no public ref maps to raw index %d", rawIdx)
	return nil
}

func TestCompactProjectionOnReportedCardShapes(t *testing.T) {
	builder := newCap(1080, 2400)
	rootIdx := builder.root()

	// A card whose content description repeats the rows its children publish.
	card := clickable(defaultEmptyRaw(rect(0, 0, 1080, 400), "android.widget.LinearLayout"))
	card.Description = "创作者小助手, 16:48 发布, 68"
	cardIdx := builder.add(rootIdx, card)
	coveredA := builder.add(cardIdx,
		withText(defaultEmptyRaw(rect(20, 20, 600, 80), "android.widget.TextView"), "创作者小助手"))
	coveredB := builder.add(cardIdx,
		withText(defaultEmptyRaw(rect(20, 90, 600, 150), "android.widget.TextView"), "68"))
	independent := builder.add(cardIdx,
		withText(defaultEmptyRaw(rect(20, 160, 600, 220), "android.widget.TextView"), "48"))

	// An unlabeled tappable shell with a source resource hint and one text child.
	shell := clickable(defaultEmptyRaw(rect(0, 500, 1080, 700), "android.widget.FrameLayout"))
	shellIdx := builder.add(rootIdx, withRes(shell, "app:id/Answer:Card:OpenUrl:content_card:5:negativeFeedback"))
	shellText := builder.add(shellIdx,
		withText(defaultEmptyRaw(rect(40, 520, 700, 600), "android.widget.TextView"), "歌手"))

	// A decorative cover image with neither label nor action.
	cover := builder.add(rootIdx, defaultEmptyRaw(rect(0, 800, 1080, 1800), "android.widget.ImageView"))

	snap := buildOne(t, NewBuilder(), builder.capture())
	compact := ProjectCompactTree(snap.Tree)
	if err := compact.Validate(); err != nil {
		t.Fatalf("compact tree must satisfy the compact view contract: %v", err)
	}

	cardRead := readNodeAt(t, compact, snap, cardIdx)
	if cardRead == nil || cardRead.Role != protocol.RoleButton ||
		len(cardRead.Label) != 1 || cardRead.Label[0] != "创作者小助手, 16:48 发布, 68" {
		t.Fatalf("composite card = %+v, want one labelled button", cardRead)
	}
	for _, rawIdx := range []int{coveredA, coveredB} {
		if node := readNodeAt(t, compact, snap, rawIdx); node != nil {
			t.Fatalf("covered row %d must be pruned: %+v", rawIdx, node)
		}
	}
	if node := readNodeAt(t, compact, snap, independent); node == nil {
		t.Fatalf("independent row %d must survive", independent)
	}

	shellRead := readNodeAt(t, compact, snap, shellIdx)
	if shellRead == nil || shellRead.Role != protocol.RoleButton ||
		len(shellRead.Label) != 1 || shellRead.Label[0] != "歌手" {
		t.Fatalf("tappable shell = %+v, want one hoisted button", shellRead)
	}
	if shellRead.Resource != nil {
		t.Fatalf("hoisted shell must drop its resource hint: %q", *shellRead.Resource)
	}
	if len(shellRead.Children) != 0 {
		t.Fatalf("hoisted shell children = %d, want none", len(shellRead.Children))
	}
	if node := readNodeAt(t, compact, snap, shellText); node != nil {
		t.Fatalf("the hoisted text child must be folded away: %+v", node)
	}

	if node := readNodeAt(t, compact, snap, cover); node != nil {
		t.Fatalf("the unlabeled cover image must be pruned: %+v", node)
	}

	// The complete tree still carries every reported node for inspect and tap.
	detailed := ProjectDetailedTree(snap.Tree)
	coverRef := ""
	for ref, index := range snap.Source {
		if index == cover {
			coverRef = ref
		}
	}
	if coverRef == "" {
		t.Fatal("the cover image must keep a public ref in the complete tree")
	}
	if node := childOf(t, detailed.Root, coverRef); node == nil {
		t.Fatal("detailed view must keep the pruned cover image")
	}
	if node := childOf(t, detailed.Root, cardRead.Ref); node == nil {
		t.Fatal("detailed view must keep the card")
	}
}

func TestInferVisualHintOnCapturedTree(t *testing.T) {
	builder := newCap(1000, 1000)
	rootIdx := builder.root()
	builder.add(rootIdx, defaultEmptyRaw(rect(0, 0, 1000, 600), "android.widget.ImageView"))
	builder.add(rootIdx,
		withText(defaultEmptyRaw(rect(0, 700, 1000, 760), "android.widget.TextView"), "封面"))

	snap := buildOne(t, NewBuilder(), builder.capture())
	kind, value := InferVisualHint(snap.Tree, "com.example.gallery")
	if kind != VisualKindImageHeavy || value != "image_heavy(60%)" {
		t.Fatalf("captured page hint = (%q,%q), want (image_heavy,image_heavy(60%%))", kind, value)
	}
}
