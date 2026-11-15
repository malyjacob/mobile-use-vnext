package engine

import (
	"context"
	"strconv"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const b05NodeCount = 20_000

func b05FlatCapture(display device.Display, rootClass string,
	count int, label string) device.Capture {
	children := make([]int, 0, count)
	nodes := make([]device.RawNode, 0, count+1)
	root := revalRoot(display.ID)
	root.ClassName = rootClass
	nodes = append(nodes, root)
	bounds := device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300}
	for index := 1; index <= count; index++ {
		children = append(children, index)
		node := revalButton(index, 0, 1, display.ID, bounds,
			label, "", "b05_target")
		node.TextSet, node.DescSet, node.ResourceSet = true, true, true
		nodes = append(nodes, node)
	}
	nodes[0].Children = children
	return revalCapture(display, nodes...)
}

func b05SetTarget(capture *device.Capture, index int) {
	node := &capture.Nodes[index]
	node.Text = "Target"
}

func b05Tier3Capture(display device.Display, fresh bool) (device.Capture, int) {
	nodes := make([]device.RawNode, 0, b05NodeCount+3)
	root := revalRoot(display.ID)
	nodes = append(nodes, root)
	children := make([]int, 0, b05NodeCount)
	anchorIndex := b05NodeCount - 1
	for index := 1; index < anchorIndex; index++ {
		children = append(children, index)
		node := revalButton(index, 0, 1, display.ID,
			device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300},
			"Other", "", "b05_other")
		node.TextSet, node.DescSet, node.ResourceSet = true, true, true
		nodes = append(nodes, node)
	}
	anchor := transparentNode(anchorIndex, 0, 1, display.ID,
		device.Rect{Left: 0, Top: 100, Right: 1080, Bottom: 700})
	anchor.ResourceID = "b05_anchor"
	nodes = append(nodes, anchor)
	target := tierTarget(0, anchorIndex, 2, display.ID,
		device.Rect{Left: 100, Top: 200, Right: 300, Bottom: 300},
		"Target", true)
	if fresh {
		wrapperIndex := len(nodes)
		wrapper := transparentNode(wrapperIndex, anchorIndex, 2, display.ID,
			device.Rect{Left: 90, Top: 190, Right: 310, Bottom: 310})
		nodes = append(nodes, wrapper)
		target.Index = len(nodes)
		target.Parent = wrapperIndex
		target.Depth = 3
		anchor.Children = []int{wrapperIndex}
		nodes[anchorIndex] = anchor
		nodes = append(nodes, target)
		children = append(children, anchorIndex, wrapperIndex)
	} else {
		target.Index = len(nodes)
		anchor.Children = []int{target.Index}
		nodes[anchorIndex] = anchor
		nodes = append(nodes, target)
		children = append(children, anchorIndex)
	}
	nodes[0].Children = children
	return revalCapture(display, nodes...), target.Index
}

func b05ReportMatchMetrics(b *testing.B, match actMatch,
	original, fresh *device.Capture) {
	b.Helper()
	b05AssertMatcherVisitBound(b, match, original, fresh)
	totalNodes := len(original.Nodes) + len(fresh.Nodes)
	b.ReportMetric(float64(match.stats.candidateVisits), "candidate_visits/op")
	b.ReportMetric(float64(match.stats.lineageSteps), "lineage_steps/op")
	b.ReportMetric(float64(match.stats.tiers), "tiers/op")
	b.ReportMetric(float64(totalNodes), "node_pairs/op")
	b.ReportMetric(float64(6*totalNodes),
		"candidate_ceiling/op")
}

func b05AssertMatcherVisitBound(b *testing.B, match actMatch,
	original, fresh *device.Capture) {
	b.Helper()
	if match.stats.tiers < 1 {
		b.Fatalf("matcher reported no tiers")
	}
	limit := match.stats.tiers * (len(original.Nodes) + len(fresh.Nodes))
	if match.stats.candidateVisits > limit {
		b.Fatalf("candidate visits = %d, want <= %d (%d tiers x %d node pairs)",
			match.stats.candidateVisits, limit, match.stats.tiers,
			len(original.Nodes)+len(fresh.Nodes))
	}
}

func BenchmarkActRevalidationB05(b *testing.B) {
	display := device.Display{ID: 12, Width: 1080, Height: 2400, Density: 420}

	b.Run("tier_matching/zero_candidate_20k", func(b *testing.B) {
		original := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		b05SetTarget(&original, b05NodeCount)
		fresh := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		want := &original.Nodes[b05NodeCount]
		var match actMatch
		var state actProbeState
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			match, state = matchActTarget(
				context.Background(), nil, &original, &fresh, want)
		}
		b.StopTimer()
		if state != actProbeNone {
			b.Fatalf("state = %v, want none", state)
		}
		b05ReportMatchMetrics(b, match, &original, &fresh)
	})

	b.Run("tier_matching/single_tier1_20k", func(b *testing.B) {
		original := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		b05SetTarget(&original, b05NodeCount)
		fresh := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		b05SetTarget(&fresh, b05NodeCount)
		want := &original.Nodes[b05NodeCount]
		var match actMatch
		var state actProbeState
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			match, state = matchActTarget(
				context.Background(), nil, &original, &fresh, want)
		}
		b.StopTimer()
		if state != actProbeUnique || match.tier != 1 {
			b.Fatalf("state=%v tier=%d, want unique Tier 1", state, match.tier)
		}
		b05ReportMatchMetrics(b, match, &original, &fresh)
	})

	b.Run("tier_matching/single_tier2_20k", func(b *testing.B) {
		original := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		b05SetTarget(&original, b05NodeCount)
		fresh := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		b05SetTarget(&fresh, b05NodeCount)
		fresh.Nodes[b05NodeCount].Bounds =
			device.Rect{Left: 100, Top: 208, Right: 300, Bottom: 308}
		want := &original.Nodes[b05NodeCount]
		var match actMatch
		var state actProbeState
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			match, state = matchActTarget(
				context.Background(), nil, &original, &fresh, want)
		}
		b.StopTimer()
		if state != actProbeUnique || match.tier != 2 {
			b.Fatalf("state=%v tier=%d, want unique Tier 2", state, match.tier)
		}
		b05ReportMatchMetrics(b, match, &original, &fresh)
	})

	b.Run("tier_matching/single_tier3_20k", func(b *testing.B) {
		original, _ := b05Tier3Capture(display, false)
		fresh, _ := b05Tier3Capture(display, true)
		want := &original.Nodes[b05NodeCount]
		var match actMatch
		var state actProbeState
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			match, state = matchActTarget(
				context.Background(), nil, &original, &fresh, want)
		}
		b.StopTimer()
		if state != actProbeUnique || match.tier != 3 {
			b.Fatalf("state=%v tier=%d, want unique Tier 3", state, match.tier)
		}
		b05ReportMatchMetrics(b, match, &original, &fresh)
	})

	b.Run("tier_matching/multi_candidate_20k", func(b *testing.B) {
		original := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		b05SetTarget(&original, b05NodeCount)
		fresh := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		b05SetTarget(&fresh, b05NodeCount-1)
		b05SetTarget(&fresh, b05NodeCount)
		want := &original.Nodes[b05NodeCount]
		var match actMatch
		var state actProbeState
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			match, state = matchActTarget(
				context.Background(), nil, &original, &fresh, want)
		}
		b.StopTimer()
		if state != actProbeAmbiguous {
			b.Fatalf("state = %v, want ambiguous", state)
		}
		b05ReportMatchMetrics(b, match, &original, &fresh)
	})

	b.Run("tier_matching/target_plus_carrier_20k", func(b *testing.B) {
		original := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		b05SetTarget(&original, b05NodeCount-1)
		original.Nodes[b05NodeCount-1].Text = "Carrier"
		b05SetTarget(&original, b05NodeCount)
		fresh := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Other")
		fresh.Nodes[b05NodeCount-1].Text = "Carrier"
		b05SetTarget(&fresh, b05NodeCount)
		targetWant := &original.Nodes[b05NodeCount]
		carrierWant := &original.Nodes[b05NodeCount-1]
		var targetMatch, carrierMatch actMatch
		var targetState, carrierState actProbeState
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			targetMatch, targetState = matchActTarget(
				context.Background(), nil, &original, &fresh, targetWant)
			carrierMatch, carrierState = matchActTarget(
				context.Background(), nil, &original, &fresh, carrierWant)
		}
		b.StopTimer()
		if targetState != actProbeUnique || carrierState != actProbeUnique {
			b.Fatalf("target=%v carrier=%v, want both unique", targetState, carrierState)
		}
		b05AssertMatcherVisitBound(b, targetMatch, &original, &fresh)
		b05AssertMatcherVisitBound(b, carrierMatch, &original, &fresh)
		visits := targetMatch.stats.candidateVisits + carrierMatch.stats.candidateVisits
		lineageSteps := targetMatch.stats.lineageSteps + carrierMatch.stats.lineageSteps
		totalNodes := len(original.Nodes) + len(fresh.Nodes)
		if visits > 6*totalNodes {
			b.Fatalf("target+carrier candidate visits = %d, want <= %d",
				visits, 6*totalNodes)
		}
		b.ReportMetric(float64(visits), "candidate_visits/op")
		b.ReportMetric(float64(lineageSteps), "lineage_steps/op")
		b.ReportMetric(float64(totalNodes), "node_pairs/op")
		b.ReportMetric(float64(6*totalNodes),
			"candidate_ceiling/op")
	})

	b.Run("ancestor_mismatch_depth1_20k", func(b *testing.B) {
		original := b05FlatCapture(display, "android.widget.FrameLayout",
			b05NodeCount, "Target")
		fresh := b05FlatCapture(display, "android.widget.LinearLayout",
			b05NodeCount, "Target")
		want := &original.Nodes[b05NodeCount]
		var match actMatch
		var state actProbeState
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			match, state = matchActTarget(
				context.Background(), nil, &original, &fresh, want)
		}
		b.StopTimer()
		if state != actProbeNone {
			b.Fatalf("state = %v, want none", state)
		}
		b05ReportMatchMetrics(b, match, &original, &fresh)
	})

	b.Run("carrier_safe_point_recompute", func(b *testing.B) {
		const carrierCount = 2_000
		base := device.Rect{Left: 0, Top: 0, Right: 1000, Bottom: 1000}
		target := &protocol.Node{
			Ref: "target", Role: protocol.RoleButton,
			Bounds: &protocol.Bounds{Left: base.Left, Top: base.Top,
				Right: base.Right, Bottom: base.Bottom},
		}
		rawNodes := make([]device.RawNode, carrierCount+1)
		rawNodes[0].Bounds = base
		scope := &tapScope{
			target: target, base: base,
			carrierByRef:  make(map[string]*tapCarrier, carrierCount),
			carrierByNode: make(map[*protocol.Node]*tapCarrier, carrierCount),
		}
		var fixed *tapCarrier
		for index := 0; index < carrierCount; index++ {
			rect := device.Rect{Left: 0, Top: 0, Right: 1, Bottom: 1}
			if index == carrierCount-1 {
				rect = device.Rect{Left: 180, Top: 388, Right: 500, Bottom: 435}
			}
			ref := "carrier-" + strconv.Itoa(index+1)
			node := &protocol.Node{
				Ref: ref, Role: protocol.RoleImage,
				Bounds: &protocol.Bounds{Left: rect.Left, Top: rect.Top,
					Right: rect.Right, Bottom: rect.Bottom},
			}
			rawNodes[index+1].Bounds = rect
			carrier := &tapCarrier{
				node: node, raw: &rawNodes[index+1], rawIndex: index + 1,
				order: index,
			}
			target.Children = append(target.Children, node)
			scope.carriers = append(scope.carriers, carrier)
			scope.carrierByRef[ref] = carrier
			scope.carrierByNode[node] = carrier
			if index == carrierCount-1 {
				fixed = carrier
			}
		}
		targetRaw := &rawNodes[0]
		var x, y int
		var strategy string
		var ok bool
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			x, y, strategy, ok = fixedCarrierSafePoint(scope, targetRaw, fixed)
		}
		b.StopTimer()
		if !ok || strategy != tapModeCarrierNode {
			b.Fatalf("fixed carrier recompute failed at (%d,%d), mode=%q", x, y, strategy)
		}
		b.ReportMetric(carrierCount, "carriers/op")
	})

	b.Run("scroll_replan_large_tree", func(b *testing.B) {
		screen := device.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400}
		containerRect := device.Rect{Left: 0, Top: 200, Right: 1080, Bottom: 2200}
		children := make([]int, 0, b05NodeCount+1)
		nodes := make([]device.RawNode, 0, b05NodeCount+2)
		root := revalRoot(display.ID)
		root.Children = []int{1}
		nodes = append(nodes, root)
		container := transparentNode(1, 0, 1, display.ID, containerRect)
		container.ClassName = "android.widget.ScrollView"
		container.Scrollable = true
		container.ResourceID = "b05_scroll"
		nodes = append(nodes, container)
		for index := 2; index < b05NodeCount+2; index++ {
			row := revalButton(index, 1, 2, display.ID,
				device.Rect{Left: 20, Top: 220, Right: 1060, Bottom: 280},
				"row", "", "")
			row.TextSet, row.DescSet, row.ResourceSet = true, true, true
			nodes = append(nodes, row)
			children = append(children, index)
		}
		blockerIndex := b05NodeCount + 2
		blocker := transparentNode(blockerIndex, 1, 2, display.ID,
			device.Rect{Left: 0, Top: 1200, Right: 1080, Bottom: 2200})
		blocker.ClassName = "androidx.recyclerview.widget.RecyclerView"
		blocker.Scrollable = true
		blocker.ResourceID = "b05_blocker"
		nodes = append(nodes, blocker)
		children = append(children, blockerIndex)
		nodes[1].Children = children
		capture := revalCapture(display, nodes...)
		snapshot, err := observation.NewBuilder().Build(&capture)
		if err != nil {
			b.Fatalf("build projection: %v", err)
		}
		projection := &Projection{
			Tree: snapshot.Tree, Capture: snapshot.Capture, Source: snapshot.Source,
		}
		clipped := containerRect.Intersect(screen)
		var spanCount int
		var startY, endY int
		var proven bool
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			space, ok := scrollSafeSpans(
				projection, 1, clipped, protocol.ScrollDirectionDown)
			if !ok {
				b.StopTimer()
				b.Fatal("scroll safe-span reduction failed")
			}
			spanCount = len(space.spans)
			span, hasSpan := space.longestSpan(protocol.ScrollDirectionDown)
			if !hasSpan {
				b.StopTimer()
				b.Fatal("scroll replan found no safe span")
			}
			start, end, geometryOK := scrollGeometry(
				space.safeRect(clipped, span), screen,
				protocol.ScrollDirectionDown, protocol.ScrollAmountPage)
			startY, endY = start.Y, end.Y
			proven = geometryOK &&
				space.provesSegment(start, end, clipped, screen)
		}
		b.StopTimer()
		if !proven || startY <= endY || startY >= 1200 {
			b.Fatalf("scroll replan = %d->%d proven=%v", startY, endY, proven)
		}
		b.ReportMetric(float64(spanCount), "safe_spans/op")
	})

	b.Run("text_focus_binding", func(b *testing.B) {
		baseBounds := device.Rect{Left: 100, Top: 300, Right: 900, Bottom: 400}
		buildOriginal := func() device.Capture {
			children := make([]int, 0, b05NodeCount)
			nodes := make([]device.RawNode, 0, b05NodeCount+1)
			root := revalRoot(display.ID)
			nodes = append(nodes, root)
			for index := 1; index < b05NodeCount; index++ {
				children = append(children, index)
				node := revalEditText(index, 0, 1, display.ID, baseBounds,
					"", "other", "", false)
				node.ResourceSet = true
				node.ResourceID = "other_field"
				nodes = append(nodes, node)
			}
			want := textBindingWant(display, baseBounds)
			want.Index = b05NodeCount
			want.Parent = 0
			want.Depth = 1
			nodes = append(nodes, want)
			children = append(children, b05NodeCount)
			nodes[0].Children = children
			return revalCapture(display, nodes...)
		}
		original := buildOriginal()
		freshFor := func(bounds device.Rect) device.Capture {
			children := make([]int, 0, b05NodeCount)
			nodes := make([]device.RawNode, 0, b05NodeCount+1)
			root := revalRoot(display.ID)
			nodes = append(nodes, root)
			for index := 1; index < b05NodeCount; index++ {
				children = append(children, index)
				node := revalEditText(index, 0, 1, display.ID, baseBounds,
					"", "other", "", false)
				node.ResourceSet = true
				node.ResourceID = "other_field"
				nodes = append(nodes, node)
			}
			want := textBindingFresh(display, bounds)
			want.Index = b05NodeCount
			want.Parent = 0
			want.Depth = 1
			nodes = append(nodes, want)
			children = append(children, b05NodeCount)
			nodes[0].Children = children
			return revalCapture(display, nodes...)
		}

		for _, mode := range []struct {
			name   string
			bounds device.Rect
			want   textBindingMode
		}{
			{"exact", baseBounds, textExact},
			{"near", device.Rect{Left: 110, Top: 310, Right: 910, Bottom: 410}, textNear},
			{"shifted", device.Rect{Left: 100, Top: 800, Right: 900, Bottom: 900}, textShifted},
		} {
			b.Run(mode.name, func(b *testing.B) {
				fresh := freshFor(mode.bounds)
				var binding textBinding
				var ok bool
				b.ReportAllocs()
				b.ResetTimer()
				for iteration := 0; iteration < b.N; iteration++ {
					binding, ok = bindFocusedText(
						&original, &fresh, &original.Nodes[b05NodeCount],
						original.Foreground, 500, 350)
				}
				b.StopTimer()
				if !ok || binding.mode != mode.want {
					b.Fatalf("binding = %+v ok=%v, want mode %q",
						binding, ok, mode.want)
				}
			})
		}
	})
}
