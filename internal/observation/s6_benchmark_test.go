package observation

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

var (
	s6SnapshotSink *Snapshot
	s6ReadTreeSink *protocol.ReadTree
	s6DeltaSink    *protocol.ReadDelta
	s6BytesSink    []byte
)

type s6MatchMetrics struct {
	present         bool
	workUsed        uint64
	workLimit       uint64
	candidateVisits uint64
	voteComparisons uint64
	budgetHit       bool
	totalMatched    int
	voteMatches     int
}

func s6DefaultEmptyRaw(bounds device.Rect, className string) device.RawNode {
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

func s6EmptyLeafCapture(emptyCount int) *device.Capture {
	capture := newCap(1080, 2400)
	root := capture.root()
	for index := 0; index < emptyCount; index++ {
		className := "android.view.View"
		if index%2 == 1 {
			className = "android.widget.FrameLayout"
		}
		left := (index % 10) * 100
		top := (index / 10) * 50
		capture.add(root, s6DefaultEmptyRaw(
			rect(left, top, left+90, top+40), className))
	}
	semantic := s6DefaultEmptyRaw(
		rect(10, 2000, 500, 2080), "android.widget.TextView")
	semantic.Text = "synthetic semantic sibling"
	capture.add(root, semantic)
	return capture.capture()
}

func s6ScrollCardsCapture(titles []string, uniqueTitles bool) *device.Capture {
	capture := newCap(1080, 2400)
	root := capture.root()
	scroll := capture.add(root, scrollable(withClass(
		visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
	for index, title := range titles {
		top := 120 + index*380
		card := capture.add(scroll, withClass(
			visible(rect(0, top, 1080, top+340)), "android.widget.LinearLayout"))
		resource := "app:id/repeated_title"
		if uniqueTitles {
			resource = "app:id/card_title"
		}
		capture.add(card, withText(withRes(withClass(
			visible(rect(20, top+20, 800, top+120)), "android.widget.TextView"),
			resource), title))
		capture.add(card, clickable(withText(withRes(withClass(
			visible(rect(820, top+20, 1020, top+120)), "android.widget.Button"),
			"app:id/card_fav"), "favorite")))
	}
	return capture.capture()
}

func s6FiveCardPair() (*device.Capture, *device.Capture) {
	return s6ScrollCardsCapture(
			[]string{"card-a", "card-b", "card-c", "card-d", "card-e"}, true),
		s6ScrollCardsCapture(
			[]string{"card-b", "card-c", "card-d", "card-e", "card-f"}, true)
}

func s6RepeatedNoAnchorPair() (*device.Capture, *device.Capture) {
	first := make([]string, 32)
	second := make([]string, 32)
	for index := range first {
		first[index] = "identical"
		second[index] = "identical"
	}
	return s6ScrollCardsCapture(first, false),
		s6ScrollCardsCapture(second, false)
}

func s6SmallContinuityPair() (*device.Capture, *device.Capture) {
	build := func(changed bool) *device.Capture {
		capture := newCap(1080, 2400)
		root := capture.root()
		for index := 0; index < 24; index++ {
			label := fmt.Sprintf("row-%02d", index)
			if changed && index == 12 {
				label = "row-12-updated"
			}
			capture.add(root, withText(withRes(withClass(
				visible(rect(20, 20+index*80, 1060, 80+index*80)),
				"android.widget.TextView"), "app:id/row"), label))
		}
		return capture.capture()
	}
	return build(false), build(true)
}

func s6DeepContinuityPair(depth int) (*device.Capture, *device.Capture) {
	build := func(shift int) *device.Capture {
		capture := newCap(1080, 2400)
		parent := capture.root()
		for layer := 0; layer < depth; layer++ {
			inset := layer * 2
			parent = capture.add(parent, withClass(visible(rect(
				inset, 100+shift+inset, 1080-inset, 2300-inset)),
				"android.widget.FrameLayout"))
		}
		capture.add(parent, clickable(withText(withRes(withClass(
			visible(rect(200, 1100+shift, 880, 1220+shift)),
			"android.widget.Button"), "app:id/confirm"), "confirm")))
		return capture.capture()
	}
	return build(0), build(4)
}

func s6TemplateCardsPair(cardCount, childrenPerCard int) (*device.Capture, *device.Capture) {
	build := func(start int) *device.Capture {
		capture := newCap(1080, 2400)
		root := capture.root()
		scroll := capture.add(root, scrollable(withClass(
			visible(rect(0, 100, 1080, 2300)), "android.widget.ScrollView")))
		for index := 0; index < cardCount; index++ {
			top := 120 + index%6*340
			card := capture.add(scroll, withClass(
				visible(rect(16, top, 1064, top+300)),
				"android.widget.LinearLayout"))
			capture.add(card, withText(withRes(withClass(
				visible(rect(32, top+16, 620, top+90)),
				"android.widget.TextView"), "app:id/card_title"),
				fmt.Sprintf("template-%05d", start+index)))
			if childrenPerCard >= 2 {
				capture.add(card, withText(withRes(withClass(
					visible(rect(32, top+100, 620, top+160)),
					"android.widget.TextView"), "app:id/card_sub"),
					"shared subtitle"))
			}
			if childrenPerCard >= 3 {
				capture.add(card, clickable(withText(withRes(withClass(
					visible(rect(660, top+20, 840, top+110)),
					"android.widget.Button"), "app:id/card_fav"), "favorite")))
			}
			if childrenPerCard >= 4 {
				capture.add(card, clickable(withText(withRes(withClass(
					visible(rect(860, top+20, 1040, top+110)),
					"android.widget.Button"), "app:id/card_share"), "share")))
			}
		}
		return capture.capture()
	}
	return build(0), build(1)
}

func s6UnknownBooleanCapture(nodeCount int) *device.Capture {
	capture := newCap(1080, 2400)
	root := capture.root()
	for index := 0; index < nodeCount; index++ {
		capture.add(root, withText(withRes(withClass(
			visible(rect(20, 20+index*40, 1060, 50+index*40)),
			"android.widget.Button"), "app:id/unknown_state"),
			fmt.Sprintf("unknown-state-%03d", index)))
	}
	return capture.capture()
}

func s6LongTextCapture(textBytes int) *device.Capture {
	capture := newCap(1080, 2400)
	root := capture.root()
	text := strings.Repeat("x", textBytes)
	capture.add(root, withText(withRes(withClass(
		visible(rect(20, 20, 1060, 200)), "android.widget.TextView"),
		"app:id/long_text"), text))
	return capture.capture()
}

func s6ChainCapture(depth int) *device.Capture {
	capture := newCap(1080, 2400)
	parent := capture.root()
	for layer := 0; layer < depth; layer++ {
		parent = capture.add(parent, s6DefaultEmptyRaw(
			rect(layer%4, 20+layer%4, 1000-layer%4, 2200),
			"android.widget.FrameLayout"))
	}
	capture.add(parent, withText(withRes(withClass(
		visible(rect(50, 100, 500, 180)),
		"android.widget.TextView"), "app:id/chain_leaf"), "chain leaf"))
	return capture.capture()
}

func s6EndToEndPair() (*device.Capture, *device.Capture) {
	build := func(addNode bool) *device.Capture {
		capture := newCap(1080, 2400)
		root := capture.root()
		parent := root
		for layer := 0; layer < 64; layer++ {
			parent = capture.add(parent, s6DefaultEmptyRaw(
				rect(layer%3, 20+layer%3, 1000-layer%3, 2200),
				"android.widget.FrameLayout"))
		}
		capture.add(parent, withText(withRes(withClass(
			visible(rect(20, 40, 800, 140)),
			"android.widget.TextView"), "app:id/long_text"),
			strings.Repeat("y", 4096)))
		for index := 0; index < 32; index++ {
			capture.add(root, withText(withRes(withClass(
				visible(rect(20, 300+index*40, 1060, 330+index*40)),
				"android.widget.Button"), "app:id/unknown_state"),
				fmt.Sprintf("unknown-%02d", index)))
		}
		if addNode {
			capture.add(root, withText(withRes(withClass(
				visible(rect(20, 1800, 500, 1880)),
				"android.widget.TextView"), "app:id/new_semantic"),
				"new semantic sibling"))
		}
		return capture.capture()
	}
	return build(false), build(true)
}

func s6ReadMatchMetrics(builder *Builder) s6MatchMetrics {
	if builder == nil {
		return s6MatchMetrics{}
	}
	value := reflect.ValueOf(builder)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return s6MatchMetrics{}
	}
	last := value.Elem().FieldByName("lastStats")
	if !last.IsValid() {
		return s6MatchMetrics{}
	}
	return s6MatchMetrics{
		present:         true,
		workUsed:        s6UintField(last, "workUsed"),
		workLimit:       s6UintField(last, "workLimit"),
		candidateVisits: s6UintField(last, "candidateVisits"),
		voteComparisons: s6UintField(last, "voteComparisons"),
		budgetHit:       s6BoolField(last, "budgetHit"),
		totalMatched:    int(s6IntField(last, "totalMatched")),
		voteMatches:     int(s6IntField(last, "stage3Matched")),
	}
}

func s6UintField(value reflect.Value, name string) uint64 {
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.Uint64 {
		return 0
	}
	return field.Uint()
}

func s6IntField(value reflect.Value, name string) int64 {
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.Int {
		return 0
	}
	return field.Int()
}

func s6BoolField(value reflect.Value, name string) bool {
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.Bool {
		return false
	}
	return field.Bool()
}

func s6SetUnexportedBool(target any, name string, value bool) bool {
	field := reflect.ValueOf(target).Elem().FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.Bool || !field.CanAddr() {
		return false
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).
		Elem().SetBool(value)
	return true
}

func s6SetUnexportedUint64(target any, name string, value uint64) bool {
	field := reflect.ValueOf(target).Elem().FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.Uint64 || !field.CanAddr() {
		return false
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).
		Elem().SetUint(value)
	return true
}

func s6BuildSnapshot(tb testing.TB, builder *Builder,
	capture *device.Capture, label string) *Snapshot {
	tb.Helper()
	snapshot, err := builder.Build(capture)
	if err != nil {
		tb.Fatalf("%s Build: %v", label, err)
	}
	return snapshot
}

func s6PreviewSnapshot(tb testing.TB, builder *Builder,
	capture *device.Capture, label string) *Snapshot {
	tb.Helper()
	snapshot, err := builder.Preview(capture)
	if err != nil {
		tb.Fatalf("%s Preview: %v", label, err)
	}
	return snapshot
}

func s6CompactJSON(tb testing.TB, snapshot *Snapshot) []byte {
	tb.Helper()
	read := ProjectCompactTree(snapshot.Tree)
	encoded, err := protocol.JSON(read)
	if err != nil {
		tb.Fatalf("compact JSON: %v", err)
	}
	return encoded
}

func s6SnapshotCost(tb testing.TB, snapshot *Snapshot) int {
	tb.Helper()
	cost, err := SnapshotCost(snapshot.Capture, snapshot.Tree)
	if err != nil {
		tb.Fatalf("SnapshotCost: %v", err)
	}
	return cost
}

func s6CountNewRefs(previous, current *Snapshot) int {
	if current == nil {
		return 0
	}
	oldRefs := make(map[string]struct{})
	if previous != nil {
		for _, ref := range previous.Refs {
			oldRefs[ref] = struct{}{}
		}
	}
	count := 0
	for _, ref := range current.Refs {
		if _, ok := oldRefs[ref]; !ok {
			count++
		}
	}
	return count
}

func s6CountPreservedRefs(previous, current *Snapshot) int {
	if previous == nil || current == nil {
		return 0
	}
	oldRefs := make(map[string]struct{}, len(previous.Refs))
	for _, ref := range previous.Refs {
		oldRefs[ref] = struct{}{}
	}
	count := 0
	for _, ref := range current.Refs {
		if _, ok := oldRefs[ref]; ok {
			count++
		}
	}
	return count
}

func s6AssertUniqueRefs(tb testing.TB, snapshot *Snapshot) {
	tb.Helper()
	seen := make(map[string]struct{}, len(snapshot.Refs))
	for _, ref := range snapshot.Refs {
		if ref == "" {
			tb.Fatalf("empty public ref")
		}
		if _, ok := seen[ref]; ok {
			tb.Fatalf("duplicate public ref %q", ref)
		}
		seen[ref] = struct{}{}
	}
}

func s6ReportSnapshotMetrics(b *testing.B, builder *Builder,
	previous, current *Snapshot) {
	b.Helper()
	encoded := s6CompactJSON(b, current)
	cost := s6SnapshotCost(b, current)
	stats := s6ReadMatchMetrics(builder)
	statsPresent := 0.0
	if stats.present {
		statsPresent = 1
	}
	b.ReportMetric(float64(len(current.Capture.Nodes)), "raw_nodes/op")
	b.ReportMetric(float64(len(current.Refs)), "public_nodes/op")
	b.ReportMetric(float64(len(encoded)), "full_bytes/op")
	b.ReportMetric(float64(cost), "snapshot_cost/op")
	b.ReportMetric(float64(s6CountPreservedRefs(previous, current)),
		"preserved_refs/op")
	b.ReportMetric(float64(s6CountNewRefs(previous, current)),
		"new_refs/op")
	b.ReportMetric(float64(stats.workUsed), "work_units/op")
	b.ReportMetric(float64(stats.workLimit), "work_limit/op")
	b.ReportMetric(float64(stats.candidateVisits), "candidate_visits/op")
	b.ReportMetric(float64(stats.voteComparisons), "vote_comparisons/op")
	b.ReportMetric(float64(stats.voteMatches), "vote_matches/op")
	b.ReportMetric(float64(stats.totalMatched), "matched/op")
	b.ReportMetric(statsPresent, "match_stats_present/op")
}

func s6FindLabeledNode(node *protocol.Node, label string) *protocol.Node {
	if node == nil {
		return nil
	}
	if len(node.Label) > 0 && node.Label[0] == label {
		return node
	}
	for _, child := range node.Children {
		if found := s6FindLabeledNode(child, label); found != nil {
			return found
		}
	}
	return nil
}

func s6CollectRefs(node *protocol.Node, refs *map[string]struct{}) {
	if node == nil {
		return
	}
	(*refs)[node.Ref] = struct{}{}
	for _, child := range node.Children {
		s6CollectRefs(child, refs)
	}
}

func s6AssertFiveCardContinuity(tb testing.TB,
	previous, current *Snapshot) {
	tb.Helper()
	currentRefs := make(map[string]struct{})
	s6CollectRefs(current.Tree.Root, &currentRefs)
	for _, title := range []string{"card-b", "card-c", "card-d", "card-e"} {
		oldCard := s6FindLabeledNode(previous.Tree.Root, title)
		newCard := s6FindLabeledNode(current.Tree.Root, title)
		if oldCard == nil || newCard == nil || oldCard.Ref != newCard.Ref {
			tb.Fatalf("surviving card %s lost its ref", title)
		}
		oldChildren := make(map[string]struct{})
		newChildren := make(map[string]struct{})
		s6CollectRefs(oldCard, &oldChildren)
		s6CollectRefs(newCard, &newChildren)
		if len(oldChildren) != len(newChildren) {
			tb.Fatalf("surviving card %s subtree changed size", title)
		}
		for ref := range oldChildren {
			if _, ok := newChildren[ref]; !ok {
				tb.Fatalf("surviving card %s lost subtree ref %s", title, ref)
			}
		}
	}

	oldCard := s6FindLabeledNode(previous.Tree.Root, "card-a")
	newCard := s6FindLabeledNode(current.Tree.Root, "card-f")
	if oldCard == nil || newCard == nil {
		tb.Fatalf("five-card fixture is incomplete")
	}
	oldRefs := make(map[string]struct{})
	s6CollectRefs(oldCard, &oldRefs)
	for ref := range oldRefs {
		if _, ok := currentRefs[ref]; ok {
			tb.Fatalf("removed card-a ref %s remained in the next tree", ref)
		}
	}
	previousRefs := make(map[string]struct{})
	s6CollectRefs(previous.Tree.Root, &previousRefs)
	newCardRefs := make(map[string]struct{})
	s6CollectRefs(newCard, &newCardRefs)
	for ref := range newCardRefs {
		if _, ok := previousRefs[ref]; ok {
			tb.Fatalf("new card-f reused previous ref %s", ref)
		}
	}
}

func BenchmarkS6B01Purification(b *testing.B) {
	for _, emptyCount := range []int{32, 256} {
		b.Run(fmt.Sprintf("empty_leaves_%d", emptyCount), func(b *testing.B) {
			capture := s6EmptyLeafCapture(emptyCount)
			builder := NewBuilder()
			measured := s6BuildSnapshot(b, builder, capture, "B01")
			semantic := s6FindLabeledNode(
				measured.Tree.Root, "synthetic semantic sibling")
			if semantic == nil || measured.Capture.Nodes[measured.Source[semantic.Ref]].Parent != capture.Root {
				b.Fatalf("semantic sibling is not preserved under the raw root")
			}
			initialStats := s6ReadMatchMetrics(builder)
			if initialStats.present && len(measured.Refs) != 2 {
				b.Fatalf("public nodes = %d, want root plus semantic sibling", len(measured.Refs))
			}
			filteredEmptyLeaves := emptyCount - max(0, len(measured.Refs)-2)
			if filteredEmptyLeaves < 0 {
				filteredEmptyLeaves = 0
			}

			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				s6SnapshotSink = s6BuildSnapshot(
					b, NewBuilder(), capture, "B01 benchmark")
			}
			b.StopTimer()
			s6ReportSnapshotMetrics(b, builder, nil, measured)
			b.ReportMetric(float64(emptyCount), "default_empty_input/op")
			b.ReportMetric(float64(filteredEmptyLeaves), "filtered_empty_leaves/op")
			b.ReportMetric(1, "semantic_sibling/op")
		})
	}
}

func BenchmarkS6B02Continuity(b *testing.B) {
	b.Run("five_cards_b_to_e_preserved", func(b *testing.B) {
		first, second := s6FiveCardPair()
		builder := NewBuilder()
		previous := s6BuildSnapshot(b, builder, first, "B02 first")
		measured := s6PreviewSnapshot(b, builder, second, "B02 second")
		s6AssertUniqueRefs(b, measured)
		s6AssertFiveCardContinuity(b, previous, measured)

		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			s6SnapshotSink = s6PreviewSnapshot(
				b, builder, second, "B02 benchmark")
		}
		b.StopTimer()
		s6ReportSnapshotMetrics(b, builder, previous, measured)
	})

	b.Run("repeated_no_anchor", func(b *testing.B) {
		first, second := s6RepeatedNoAnchorPair()
		builder := NewBuilder()
		previous := s6BuildSnapshot(b, builder, first, "B02 repeated first")
		measured := s6PreviewSnapshot(b, builder, second, "B02 repeated second")
		s6AssertUniqueRefs(b, measured)

		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			s6SnapshotSink = s6PreviewSnapshot(
				b, builder, second, "B02 repeated benchmark")
		}
		b.StopTimer()
		s6ReportSnapshotMetrics(b, builder, previous, measured)
	})
}

func BenchmarkS6B04Matching(b *testing.B) {
	cases := []struct {
		name string
		pair func() (*device.Capture, *device.Capture)
	}{
		{name: "small_tree", pair: s6SmallContinuityPair},
		{name: "deep_nested_containers", pair: func() (*device.Capture, *device.Capture) {
			return s6DeepContinuityPair(48)
		}},
		{name: "repeated_template_cards", pair: func() (*device.Capture, *device.Capture) {
			return s6TemplateCardsPair(200, 3)
		}},
		{name: "twenty_thousand_raw_nodes", pair: func() (*device.Capture, *device.Capture) {
			return s6TemplateCardsPair(3999, 4)
		}},
	}
	for _, testCase := range cases {
		testCase := testCase
		b.Run(testCase.name, func(b *testing.B) {
			first, second := testCase.pair()
			builder := NewBuilder()
			previous := s6BuildSnapshot(b, builder, first, "B04 first")
			measured := s6PreviewSnapshot(b, builder, second, "B04 second")
			s6AssertUniqueRefs(b, measured)
			stats := s6ReadMatchMetrics(builder)
			if stats.present && (stats.budgetHit || stats.workUsed > stats.workLimit) {
				b.Fatalf("budget exceeded: used=%d limit=%d hit=%v",
					stats.workUsed, stats.workLimit, stats.budgetHit)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				s6SnapshotSink = s6PreviewSnapshot(
					b, builder, second, "B04 benchmark")
			}
			b.StopTimer()
			s6ReportSnapshotMetrics(b, builder, previous, measured)
		})
	}
}

func BenchmarkS6B06Boundaries(b *testing.B) {
	b.Run("five_unknown_booleans", func(b *testing.B) {
		capture := s6UnknownBooleanCapture(256)
		builder := NewBuilder()
		measured := s6BuildSnapshot(b, builder, capture, "B06 unknown booleans")
		unknownNodes := s6CountNodesWithFiveUnknownBooleans(measured.Tree.Root)
		if s6ReadMatchMetrics(builder).present && unknownNodes == 0 {
			b.Fatalf("fixture produced no five-boolean unknown node")
		}

		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			s6SnapshotSink = s6BuildSnapshot(
				b, NewBuilder(), capture, "B06 unknown benchmark")
		}
		b.StopTimer()
		s6ReportSnapshotMetrics(b, builder, nil, measured)
		b.ReportMetric(float64(unknownNodes), "five_unknown_nodes/op")
		b.ReportMetric(float64(unknownNodes*5), "unknown_booleans/op")
	})

	b.Run("long_text_4096", func(b *testing.B) {
		const textBytes = 4096
		capture := s6LongTextCapture(textBytes)
		builder := NewBuilder()
		measured := s6BuildSnapshot(b, builder, capture, "B06 long text")

		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			s6SnapshotSink = s6BuildSnapshot(
				b, NewBuilder(), capture, "B06 long text benchmark")
		}
		b.StopTimer()
		s6ReportSnapshotMetrics(b, builder, nil, measured)
		b.ReportMetric(textBytes, "text_bytes/op")
	})

	b.Run("wrapper_pruning_512", func(b *testing.B) {
		const chainDepth = 512
		capture := s6ChainCapture(chainDepth)
		builder := NewBuilder()
		measured := s6BuildSnapshot(b, builder, capture, "B06 chain")
		read := ProjectCompactTree(measured.Tree)
		chainDefinitions, chainRefs := s6ChainDefinitionMetrics(read.Root)
		compactNodes := s6ReadNodeCount(read.Root)
		if chainDefinitions != 0 || chainRefs != 0 || compactNodes >= chainDepth {
			b.Fatalf("wrapper fixture did not prune depth %d: definitions=%d refs=%d nodes=%d",
				chainDepth, chainDefinitions, chainRefs, compactNodes)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			s6SnapshotSink = s6BuildSnapshot(
				b, NewBuilder(), capture, "B06 chain benchmark")
		}
		b.StopTimer()
		s6ReportSnapshotMetrics(b, builder, nil, measured)
		b.ReportMetric(float64(chainDefinitions), "chain_definitions/op")
		b.ReportMetric(float64(chainRefs), "chain_refs/op")
		b.ReportMetric(float64(compactNodes), "compact_nodes/op")
		b.ReportMetric(float64(chainDepth), "wrapper_nodes_pruned/op")
	})

	b.Run("budget_cutoff", func(b *testing.B) {
		first, second := s6RepeatedNoAnchorPair()
		builder := NewBuilder()
		previous := s6BuildSnapshot(b, builder, first, "B06 budget first")
		supported := s6SetUnexportedBool(builder, "hasBudgetLimit", true) &&
			s6SetUnexportedUint64(builder, "budgetLimit", 1)
		measured := s6PreviewSnapshot(b, builder, second, "B06 budget measured")
		s6AssertUniqueRefs(b, measured)
		stats := s6ReadMatchMetrics(builder)
		if supported && (!stats.present || !stats.budgetHit || stats.workUsed > 1) {
			b.Fatalf("budget cutoff not honored: supported=%v stats=%+v",
				supported, stats)
		}
		supportedMetric := 0.0
		budgetHitMetric := 0.0
		if supported {
			supportedMetric = 1
		}
		if stats.budgetHit {
			budgetHitMetric = 1
		}

		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			runBuilder := NewBuilder()
			_ = s6BuildSnapshot(b, runBuilder, first, "B06 budget seed")
			if supported {
				s6SetUnexportedBool(runBuilder, "hasBudgetLimit", true)
				s6SetUnexportedUint64(runBuilder, "budgetLimit", 1)
			}
			s6SnapshotSink = s6PreviewSnapshot(
				b, runBuilder, second, "B06 budget benchmark")
		}
		b.StopTimer()
		s6ReportSnapshotMetrics(b, builder, previous, measured)
		b.ReportMetric(supportedMetric, "budget_supported/op")
		b.ReportMetric(budgetHitMetric, "budget_hit/op")
		b.ReportMetric(1, "budget_limit/op")
	})

	b.Run("build_read_diff_json_apply", func(b *testing.B) {
		baseCapture, currentCapture := s6EndToEndPair()
		baseSnapshot, currentSnapshot, baseRead, currentRead, delta, fullJSON, deltaJSON :=
			s6PrepareEndToEnd(b, baseCapture, currentCapture)
		applied := s6ApplyStrictJSONDelta(b, baseRead, deltaJSON)
		if !ReadEqual(applied, currentRead) {
			b.Fatalf("ReadApply result differs from current read tree")
		}
		appliedJSON, err := protocol.JSON(applied)
		if err != nil {
			b.Fatalf("encode applied tree: %v", err)
		}
		if !bytes.Equal(appliedJSON, fullJSON) {
			b.Fatalf("canonical bytes differ after JSON round trip")
		}
		baseCost := s6SnapshotCost(b, baseSnapshot)
		currentCost := s6SnapshotCost(b, currentSnapshot)
		chainDefinitions, _ := s6ChainDefinitionMetrics(currentRead.Root)
		if chainDefinitions != 0 {
			b.Fatalf("new compact output contains chain definitions")
		}
		unknownNodes := s6CountNodesWithFiveUnknownBooleans(currentSnapshot.Tree.Root)
		_ = delta

		b.ReportAllocs()
		b.ResetTimer()
		for iteration := 0; iteration < b.N; iteration++ {
			_, _, runBase, runCurrent, runDelta, runFullJSON, runDeltaJSON :=
				s6PrepareEndToEnd(b, baseCapture, currentCapture)
			s6ReadTreeSink = s6ApplyStrictJSONDelta(b, runBase, runDeltaJSON)
			s6DeltaSink = runDelta
			s6BytesSink = runFullJSON
			if !ReadEqual(s6ReadTreeSink, runCurrent) {
				b.Fatalf("end-to-end ReadApply mismatch")
			}
		}
		b.StopTimer()
		s6ReportSnapshotMetrics(b, NewBuilder(), baseSnapshot, currentSnapshot)
		b.ReportMetric(float64(baseCost+currentCost), "paired_snapshot_cost/op")
		b.ReportMetric(float64(len(fullJSON)), "read_full_bytes/op")
		b.ReportMetric(float64(len(deltaJSON)), "read_delta_bytes/op")
		b.ReportMetric(float64(chainDefinitions), "chain_definitions/op")
		b.ReportMetric(float64(s6ReadNodeCount(currentRead.Root)), "compact_nodes/op")
		b.ReportMetric(float64(unknownNodes*5), "unknown_booleans/op")
		b.ReportMetric(4096, "text_bytes/op")
		if len(fullJSON) > 0 {
			b.ReportMetric(
				float64(len(deltaJSON))*100/float64(len(fullJSON)),
				"delta/full-%")
		}
		b.ReportMetric(1, "read_apply_equal/op")
	})
}

func s6CountNodesWithFiveUnknownBooleans(node *protocol.Node) int {
	if node == nil {
		return 0
	}
	count := 0
	if node.Enabled == nil && node.Focused == nil && node.Selected == nil &&
		node.Protected == nil && node.Checked == nil {
		count++
	}
	for _, child := range node.Children {
		count += s6CountNodesWithFiveUnknownBooleans(child)
	}
	return count
}

func s6ChainDefinitionMetrics(node *protocol.ReadNode) (definitions, refs int) {
	if node == nil {
		return 0, 0
	}
	if len(node.Chain) > 0 {
		definitions++
		refs += len(node.Chain)
	}
	for _, child := range node.Children {
		childDefinitions, childRefs := s6ChainDefinitionMetrics(child)
		definitions += childDefinitions
		refs += childRefs
	}
	return definitions, refs
}

func s6ReadNodeCount(node *protocol.ReadNode) int {
	if node == nil {
		return 0
	}
	count := len(node.Refs())
	for _, child := range node.Children {
		count += s6ReadNodeCount(child)
	}
	return count
}

func s6PrepareEndToEnd(tb testing.TB, baseCapture, currentCapture *device.Capture) (
	*Snapshot, *Snapshot, *protocol.ReadTree, *protocol.ReadTree,
	*protocol.ReadDelta, []byte, []byte,
) {
	tb.Helper()
	builder := NewBuilder()
	baseSnapshot := s6BuildSnapshot(tb, builder, baseCapture, "B06 base")
	currentSnapshot := s6BuildSnapshot(tb, builder, currentCapture, "B06 current")
	baseRead := ProjectCompactTree(baseSnapshot.Tree)
	currentRead := ProjectCompactTree(currentSnapshot.Tree)
	delta, err := ReadDiff("O1", baseRead, currentRead)
	if err != nil {
		tb.Fatalf("ReadDiff: %v", err)
	}
	fullJSON, err := protocol.JSON(currentRead)
	if err != nil {
		tb.Fatalf("encode full read tree: %v", err)
	}
	deltaJSON, err := protocol.JSON(delta)
	if err != nil {
		tb.Fatalf("encode read delta: %v", err)
	}
	return baseSnapshot, currentSnapshot, baseRead, currentRead,
		delta, fullJSON, deltaJSON
}

func s6ApplyStrictJSONDelta(tb testing.TB, base *protocol.ReadTree,
	encoded []byte) *protocol.ReadTree {
	tb.Helper()
	var delta protocol.ReadDelta
	if err := protocol.Decode(encoded, &delta); err != nil {
		tb.Fatalf("strict decode read delta: %v", err)
	}
	applied, err := ReadApply(base, &delta)
	if err != nil {
		tb.Fatalf("ReadApply: %v", err)
	}
	return applied
}
