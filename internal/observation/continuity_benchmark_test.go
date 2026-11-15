package observation

import (
	"fmt"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func fiveCardScrollPair() (frame1, frame2 *device.Capture) {
	frame1 = scrollCardsCapture([]string{"卡片 A", "卡片 B", "卡片 C", "卡片 D", "卡片 E"}, 0, 1)
	frame2 = scrollCardsCapture([]string{"卡片 B", "卡片 C", "卡片 D", "卡片 E", "卡片 F"}, 1, 1)
	return frame1, frame2
}

func smallContinuityPair() (frame1, frame2 *device.Capture) {
	build := func(second bool) *device.Capture {
		capBuilder := newCap(1080, 2400)
		rootNode := capBuilder.root()
		header := capBuilder.add(rootNode, withText(withRes(withClass(visible(rect(0, 80, 1080, 180)), "android.widget.TextView"), "app:id/header"), "首页"))
		_ = header
		labels := []string{"推荐", "关注", "设置"}
		if second {
			labels = []string{"推荐", "消息", "设置"}
		}
		for idx, label := range labels {
			top := 220 + idx*180
			row := capBuilder.add(rootNode, withClass(visible(rect(20, top, 1060, top+140)), "android.widget.LinearLayout"))
			capBuilder.add(row, withText(withClass(visible(rect(40, top+20, 680, top+110)), "android.widget.TextView"), label))
			capBuilder.add(row, clickable(withRes(withClass(withText(visible(rect(740, top+20, 980, top+110)), "打开"), "android.widget.Button"), "app:id/open")))
		}
		return capBuilder.capture()
	}
	return build(false), build(true)
}

func deepNestedContainersPair(depth int) (frame1, frame2 *device.Capture) {
	build := func(shiftY int, auxLabel string) *device.Capture {
		capBuilder := newCap(1080, 2400)
		curParent := capBuilder.root()
		for layer := 0; layer < depth; layer++ {
			inset := layer * 2
			curParent = capBuilder.add(curParent, withClass(visible(rect(inset, 100+shiftY+inset, 1080-inset, 2300-inset)), "android.widget.FrameLayout"))
		}
		capBuilder.add(curParent, clickable(withRes(withClass(withText(visible(rect(200, 1100+shiftY, 880, 1220+shiftY)), "立即确认"), "android.widget.Button"), "app:id/confirm")))
		capBuilder.add(capBuilder.root(), withText(withClass(visible(rect(40, 20, 400, 90)), "android.widget.TextView"), auxLabel))
		return capBuilder.capture()
	}
	return build(0, "状态: 准备"), build(4, "状态: 就绪")
}

func scrolledTemplateCardsPair(cardCount int, childrenPerCard int) (frame1, frame2 *device.Capture) {
	build := func(startID int) *device.Capture {
		capBuilder := newCap(1080, 2400)
		rootNode := capBuilder.root()
		scroll := capBuilder.add(rootNode, scrollable(withClass(visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
		for idx := 0; idx < cardCount; idx++ {
			top := 120 + (idx%6)*340
			card := capBuilder.add(scroll, withClass(visible(rect(16, top, 1064, top+300)), "android.widget.LinearLayout"))
			capBuilder.add(card, withText(withRes(withClass(visible(rect(32, top+16, 620, top+90)), "android.widget.TextView"), "app:id/card_title"), fmt.Sprintf("模板卡片-%05d", startID+idx)))
			if childrenPerCard >= 2 {
				capBuilder.add(card, withText(withRes(withClass(visible(rect(32, top+100, 620, top+160)), "android.widget.TextView"), "app:id/card_sub"), "统一副标题"))
			}
			if childrenPerCard >= 3 {
				capBuilder.add(card, clickable(withRes(withClass(withText(visible(rect(660, top+20, 840, top+110)), "收藏"), "android.widget.Button"), "app:id/card_fav")))
			}
			if childrenPerCard >= 4 {
				capBuilder.add(card, clickable(withRes(withClass(withText(visible(rect(860, top+20, 1040, top+110)), "分享"), "android.widget.Button"), "app:id/card_share")))
			}
		}
		return capBuilder.capture()
	}
	return build(0), build(1)
}

// deepMixedLargeStressPair produces roughly 20,000 raw nodes combining deep
// anonymous chains with shifted cards so work-budget cutoffs exercise multiple matching passes.
func deepMixedLargeStressPair() (frame1, frame2 *device.Capture) {
	build := func(startID int) *device.Capture {
		capBuilder := newCap(1080, 2400)
		rootNode := capBuilder.root()
		chainParent := rootNode
		for layer := 0; layer < 40; layer++ {
			chainParent = capBuilder.add(chainParent, withClass(visible(rect(0, 100, 1080, 2300)), "android.widget.FrameLayout"))
		}
		capBuilder.add(chainParent, clickable(withRes(withClass(withText(visible(rect(100, 120, 500, 200)), "深层锚点"), "android.widget.Button"), "app:id/deep_anchor")))
		scroll := capBuilder.add(rootNode, scrollable(withClass(visible(rect(0, 220, 1080, 2300)), "android.widget.ScrollView")))
		for idx := 0; idx < 3990; idx++ {
			top := 240 + (idx%5)*380
			card := capBuilder.add(scroll, withClass(visible(rect(16, top, 1064, top+340)), "android.widget.LinearLayout"))
			capBuilder.add(card, withText(withRes(withClass(visible(rect(32, top+16, 620, top+90)), "android.widget.TextView"), "app:id/card_title"), fmt.Sprintf("混合卡片-%05d", startID+idx)))
			capBuilder.add(card, withText(withRes(withClass(visible(rect(32, top+100, 620, top+160)), "android.widget.TextView"), "app:id/card_sub"), "副标题"))
			capBuilder.add(card, clickable(withRes(withClass(withText(visible(rect(660, top+20, 840, top+110)), "收藏"), "android.widget.Button"), "app:id/card_fav")))
			capBuilder.add(card, clickable(withRes(withClass(withText(visible(rect(860, top+20, 1040, top+110)), "赞"), "android.widget.Button"), "app:id/card_like")))
		}
		return capBuilder.capture()
	}
	return build(0), build(1)
}

func runContinuityBenchmarkPair(b *testing.B, frame1, frame2 *device.Capture) {
	b.Helper()
	builder := NewBuilder()
	if _, err := builder.Build(frame1); err != nil {
		b.Fatalf("seed frame1 failed: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		if _, err := builder.Preview(frame2); err != nil {
			b.Fatalf("preview frame2 failed: %v", err)
		}
	}
	b.ReportMetric(float64(builder.lastStats.workUsed), "work/op")
}

// BenchmarkContinuityScrollFiveCards measures steady-state matching across a
// five-card scroll where four cards remain visible and one new card enters.
func BenchmarkContinuityScrollFiveCards(b *testing.B) {
	frame1, frame2 := fiveCardScrollPair()
	runContinuityBenchmarkPair(b, frame1, frame2)
}

// BenchmarkContinuityTreeShapes measures matching cost and allocations across
// representative tree topologies up to ~20,000 raw nodes.
func BenchmarkContinuityTreeShapes(b *testing.B) {
	shapes := []struct {
		name string
		pair func() (*device.Capture, *device.Capture)
	}{
		{name: "small_tree", pair: smallContinuityPair},
		{name: "deep_nested_containers", pair: func() (*device.Capture, *device.Capture) { return deepNestedContainersPair(48) }},
		{name: "repeated_template_cards", pair: func() (*device.Capture, *device.Capture) { return scrolledTemplateCardsPair(200, 3) }},
		{name: "large_twenty_thousand_raw_nodes", pair: func() (*device.Capture, *device.Capture) { return scrolledTemplateCardsPair(3999, 4) }},
	}
	for _, shape := range shapes {
		shape := shape
		b.Run(shape.name, func(b *testing.B) {
			frame1, frame2 := shape.pair()
			runContinuityBenchmarkPair(b, frame1, frame2)
		})
	}
}

func collectTreeSubtreeRefs(node *protocol.Node) []string {
	var refs []string
	var walk func(cur *protocol.Node)
	walk = func(cur *protocol.Node) {
		if cur == nil {
			return
		}
		refs = append(refs, cur.Ref)
		for _, child := range cur.Children {
			walk(child)
		}
	}
	walk(node)
	return refs
}

// TestContinuityScrollFiveCardsPreservationAndScaleBounds verifies 100% ref
// preservation on overlapping scrolled cards, 0% reuse from evicted cards,
// linear work-budget adherence up to ~20,000 raw nodes, and raw-order-invariant cutoffs.
func TestContinuityScrollFiveCardsPreservationAndScaleBounds(t *testing.T) {
	t.Run("five_card_scroll_preserves_B_to_E_and_zero_reuse_from_A", func(t *testing.T) {
		frame1, frame2 := fiveCardScrollPair()
		builder := NewBuilder()
		first := buildOne(t, builder, frame1)
		previewSnap, err := builder.Preview(frame2)
		if err != nil {
			t.Fatalf("Preview failed: %v", err)
		}
		second := buildOne(t, builder, frame2)
		if len(previewSnap.Refs) != len(second.Refs) {
			t.Fatalf("preview ref count %d != build ref count %d", len(previewSnap.Refs), len(second.Refs))
		}
		for idx := range previewSnap.Refs {
			if previewSnap.Refs[idx] != second.Refs[idx] {
				t.Fatalf("preview drifted from commit at %d: %s vs %s", idx, previewSnap.Refs[idx], second.Refs[idx])
			}
		}

		firstRefSet := make(map[string]bool, len(first.Refs))
		for _, ref := range first.Refs {
			firstRefSet[ref] = true
		}
		cardARefs := make(map[string]bool)
		for _, ref := range collectTreeSubtreeRefs(findCardByTitle(first.Tree, "卡片 A")) {
			cardARefs[ref] = true
		}

		preservedCount := 0
		totalSurvivingNodes := 0
		for _, title := range []string{"卡片 B", "卡片 C", "卡片 D", "卡片 E"} {
			oldCard := findCardByTitle(first.Tree, title)
			newCard := findCardByTitle(second.Tree, title)
			if oldCard == nil || newCard == nil {
				t.Fatalf("missing surviving card %s", title)
			}
			oldSubtree := collectTreeSubtreeRefs(oldCard)
			newSubtree := collectTreeSubtreeRefs(newCard)
			if len(oldSubtree) != len(newSubtree) {
				t.Fatalf("card %s subtree length changed: %d -> %d", title, len(oldSubtree), len(newSubtree))
			}
			for idx := range oldSubtree {
				totalSurvivingNodes++
				if oldSubtree[idx] == newSubtree[idx] {
					preservedCount++
				}
			}
		}
		if totalSurvivingNodes != 12 || preservedCount != totalSurvivingNodes {
			t.Fatalf("expected 12/12 surviving B-E refs preserved, got %d/%d", preservedCount, totalSurvivingNodes)
		}

		cardF := findCardByTitle(second.Tree, "卡片 F")
		if cardF == nil {
			t.Fatalf("missing newly entered card F")
		}
		for _, ref := range collectTreeSubtreeRefs(cardF) {
			if cardARefs[ref] || firstRefSet[ref] {
				t.Fatalf("newly entered card F reused prior ref %s", ref)
			}
		}
	})

	t.Run("tree_shapes_and_twenty_thousand_raw_nodes_within_budget", func(t *testing.T) {
		cases := []struct {
			name        string
			minRawNodes int
			pair        func() (*device.Capture, *device.Capture)
			verify      func(t *testing.T, prevSnap, curSnap *Snapshot)
		}{
			{
				name:        "small_tree",
				minRawNodes: 10,
				pair:        smallContinuityPair,
			},
			{
				name:        "deep_nested_containers",
				minRawNodes: 50,
				pair:        func() (*device.Capture, *device.Capture) { return deepNestedContainersPair(48) },
				verify: func(t *testing.T, prevSnap, curSnap *Snapshot) {
					oldCur := prevSnap.Tree.Root
					newCur := curSnap.Tree.Root
					for layer := 0; layer < 48; layer++ {
						if len(oldCur.Children) == 0 || len(newCur.Children) == 0 {
							t.Fatalf("deep chain truncated at layer %d", layer)
						}
						oldCur = oldCur.Children[0]
						newCur = newCur.Children[0]
						if oldCur.Ref != newCur.Ref {
							t.Fatalf("deep wrapper layer %d lost ref: %s -> %s", layer, oldCur.Ref, newCur.Ref)
						}
					}
					if oldCur.Children[0].Ref != newCur.Children[0].Ref {
						t.Fatalf("deep leaf confirm button lost ref: %s -> %s", oldCur.Children[0].Ref, newCur.Children[0].Ref)
					}
				},
			},
			{
				name:        "repeated_template_cards",
				minRawNodes: 800,
				pair:        func() (*device.Capture, *device.Capture) { return scrolledTemplateCardsPair(200, 3) },
				verify: func(t *testing.T, prevSnap, curSnap *Snapshot) {
					oldCard := findCardByTitle(prevSnap.Tree, "模板卡片-00100")
					newCard := findCardByTitle(curSnap.Tree, "模板卡片-00100")
					if oldCard == nil || newCard == nil || oldCard.Ref != newCard.Ref {
						t.Fatalf("middle template card lost continuity on 200-card scroll")
					}
				},
			},
			{
				name:        "large_twenty_thousand_raw_nodes",
				minRawNodes: 19_990,
				pair:        func() (*device.Capture, *device.Capture) { return scrolledTemplateCardsPair(3999, 4) },
				verify: func(t *testing.T, prevSnap, curSnap *Snapshot) {
					oldCard := findCardByTitle(prevSnap.Tree, "模板卡片-02000")
					newCard := findCardByTitle(curSnap.Tree, "模板卡片-02000")
					if oldCard == nil || newCard == nil || oldCard.Ref != newCard.Ref {
						t.Fatalf("middle card in ~20k raw tree lost continuity")
					}
				},
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				frame1, frame2 := tc.pair()
				if len(frame1.Nodes) < tc.minRawNodes || len(frame2.Nodes) < tc.minRawNodes {
					t.Fatalf("expected >= %d raw nodes, got %d and %d", tc.minRawNodes, len(frame1.Nodes), len(frame2.Nodes))
				}
				builder := NewBuilder()
				prevSnap := buildOne(t, builder, frame1)
				curSnap := buildOne(t, builder, frame2)
				assertUniqueTreeRefs(t, curSnap)
				stats := builder.lastStats
				wantMax := uint64(128*(len(prevSnap.Refs)+len(curSnap.Refs)+1) + 65_536)
				if stats.workLimit != wantMax {
					t.Fatalf("workLimit = %d, want %d", stats.workLimit, wantMax)
				}
				if stats.budgetHit || stats.workUsed > stats.workLimit {
					t.Fatalf("exceeded work budget on %s: used=%d limit=%d hit=%v stage=%s",
						tc.name, stats.workUsed, stats.workLimit, stats.budgetHit, stats.stoppedStage)
				}
				if tc.verify != nil {
					tc.verify(t, prevSnap, curSnap)
				}
			})
		}
	})

	t.Run("deep_twenty_thousand_raw_nodes_cutoff_deterministic_under_reverse_raw", func(t *testing.T) {
		frame1, frame2 := deepMixedLargeStressPair()
		if len(frame1.Nodes) < 19_900 {
			t.Fatalf("expected ~20k raw nodes, got %d", len(frame1.Nodes))
		}
		builderFull := NewBuilder()
		_ = buildOne(t, builderFull, frame1)
		_ = buildOne(t, builderFull, frame2)
		fullUsed := builderFull.lastStats.workUsed
		if fullUsed < 4 {
			t.Fatalf("unexpectedly small workUsed %d", fullUsed)
		}
		tightLimit := fullUsed / 2

		runAtLimit := func(reverseRaw bool) (*Snapshot, matchStats) {
			frame2Copy := frame2
			if reverseRaw {
				frame2Copy = reorderCaptureRawNonRootReverse(frame2)
			}
			builder := NewBuilder()
			_ = buildOne(t, builder, frame1)
			builder.hasBudgetLimit = true
			builder.budgetLimit = tightLimit
			snap := buildOne(t, builder, frame2Copy)
			return snap, builder.lastStats
		}

		snapNorm, statsNorm := runAtLimit(false)
		snapRev, statsRev := runAtLimit(true)
		assertUniqueTreeRefs(t, snapNorm)
		assertUniqueTreeRefs(t, snapRev)
		if !statsNorm.budgetHit || statsNorm.workUsed > tightLimit {
			t.Fatalf("expected budget cutoff at <=%d, got used=%d hit=%v", tightLimit, statsNorm.workUsed, statsNorm.budgetHit)
		}
		if statsNorm != statsRev {
			t.Fatalf("stats drifted under reverse raw order: %+v vs %+v", statsNorm, statsRev)
		}
		if len(snapNorm.Refs) != len(snapRev.Refs) {
			t.Fatalf("ref length mismatch: %d vs %d", len(snapNorm.Refs), len(snapRev.Refs))
		}
		for idx := range snapNorm.Refs {
			if snapNorm.Refs[idx] != snapRev.Refs[idx] {
				t.Fatalf("ref drifted at %d under ~20k budget cutoff: %s vs %s", idx, snapNorm.Refs[idx], snapRev.Refs[idx])
			}
		}
	})
}
