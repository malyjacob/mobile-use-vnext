package observation

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The visual hint tests cover the four precise rules of the pure classifier and
// the exact rectangle-union arithmetic behind it. Every fixture is synthetic; no
// device, image or network is involved.

func visualTree(width, height int, children ...*protocol.Node) *protocol.Tree {
	root := plainT("n1", protocol.RoleRoot)
	root.Bounds = rectT(0, 0, width, height)
	root.Children = children
	return &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
}

// visualPicture builds one unlabeled non-interactive image leaf.
func visualPicture(ref string, bounds *protocol.Bounds) *protocol.Node {
	return nodeT(ref, protocol.RoleImage, bounds)
}

// visualControl builds one labeled tappable control.
func visualControl(ref string, bounds *protocol.Bounds, label string) *protocol.Node {
	node := nodeT(ref, protocol.RoleButton, bounds, label)
	node.Actions = []protocol.Action{protocol.ActionTap}
	return node
}

// visualControlBar places five labelled controls in a bottom strip that no
// fixture picture covers, so the sparse-tree rule stays off without feeding the
// background-carrier rule.
func visualControlBar() []*protocol.Node {
	nodes := make([]*protocol.Node, 0, 5)
	for i := 0; i < 5; i++ {
		left := i * 60
		nodes = append(nodes, visualControl(
			"b"+itoa(i+1), rectT(left, 940, left+50, 980), "b"))
	}
	return nodes
}

func requireVisualHint(t *testing.T, tree *protocol.Tree, foreground, wantKind, wantValue string) {
	t.Helper()
	kind, value := InferVisualHint(tree, foreground)
	if kind != wantKind || value != wantValue {
		t.Fatalf("InferVisualHint = (%q,%q), want (%q,%q)", kind, value, wantKind, wantValue)
	}
}

func TestInferVisualHintWithoutScreen(t *testing.T) {
	requireVisualHint(t, nil, "com.example.app", "", "")
	tree := visualTree(1000, 1000, visualPicture("n2", rectT(0, 0, 1000, 900)))
	tree.Root.Bounds = nil
	requireVisualHint(t, tree, "com.example.app", "", "")
	// A degenerate screen has no denominator and therefore no hint.
	requireVisualHint(t, visualTree(0, 1000), "com.example.app", "", "")
}

func TestInferVisualHintImageHeavy(t *testing.T) {
	t.Run("exact union of two overlapping covers", func(t *testing.T) {
		tree := visualTree(1000, 1000,
			visualPicture("n2", rectT(0, 0, 1000, 500)),
			visualPicture("n3", rectT(0, 250, 1000, 750)))
		requireVisualHint(t, tree, "com.example.app", VisualKindImageHeavy, "image_heavy(75%)")
	})

	t.Run("identical covers count once", func(t *testing.T) {
		tree := visualTree(1000, 1000,
			visualPicture("n2", rectT(0, 0, 1000, 500)),
			visualPicture("n3", rectT(0, 0, 1000, 500)))
		requireVisualHint(t, tree, "com.example.app", VisualKindImageHeavy, "image_heavy(50%)")
	})

	t.Run("fragments below five percent are ignored", func(t *testing.T) {
		children := make([]*protocol.Node, 0, 16)
		top := 0
		for i := 0; i < 8; i++ {
			children = append(children, visualPicture(
				"p"+itoa(i+1), rectT(0, top, 1000, top+52))) // 5.2%
			top += 52
		}
		for i := 0; i < 4; i++ {
			children = append(children, visualPicture(
				"f"+itoa(i+1), rectT(0, top, 1000, top+49))) // 4.9%
			top += 49
		}
		children = append(children, visualControlBar()...)
		// The eight foreground pictures cover 41.6%: below the union threshold
		// once the 4.9% fragments are filtered out.
		requireVisualHint(t, visualTree(1000, 1000, children...), "com.example.app", "", "")

		// A ninth foreground picture reaches 46.8%.
		children = append(children, visualPicture("p9", rectT(0, top, 1000, top+52)))
		requireVisualHint(t, visualTree(1000, 1000, children...), "com.example.app",
			VisualKindImageHeavy, "image_heavy(47%)")
	})

	t.Run("full-screen backdrop is excluded", func(t *testing.T) {
		children := append([]*protocol.Node{
			visualPicture("n2", rectT(0, 0, 1000, 900)), // exactly 90%
		}, visualControlBar()...)
		requireVisualHint(t, visualTree(1000, 1000, children...), "com.example.app", "", "")

		// 89% is a foreground picture.
		children = append([]*protocol.Node{
			visualPicture("n2", rectT(0, 0, 1000, 890)),
		}, visualControlBar()...)
		requireVisualHint(t, visualTree(1000, 1000, children...), "com.example.app",
			VisualKindImageHeavy, "image_heavy(89%)")
	})

	t.Run("background carrier with three smaller controls is excluded", func(t *testing.T) {
		carrier := visualPicture("n2", rectT(0, 0, 1000, 500))
		children := []*protocol.Node{
			carrier,
			visualControl("c1", rectT(100, 100, 200, 180), "one"),
			visualControl("c2", rectT(300, 100, 400, 180), "two"),
			visualControl("c3", rectT(500, 100, 600, 180), "six"),
			visualControl("o1", rectT(0, 900, 50, 940), "x"),
			visualControl("o2", rectT(60, 900, 110, 940), "y"),
		}
		requireVisualHint(t, visualTree(1000, 1000, children...), "com.example.app", "", "")

		// Two inner controls are not enough to call the picture a background.
		children = []*protocol.Node{
			carrier,
			visualControl("c1", rectT(100, 100, 200, 180), "one"),
			visualControl("c2", rectT(300, 100, 400, 180), "two"),
			visualControl("o1", rectT(0, 900, 50, 940), "x"),
			visualControl("o2", rectT(60, 900, 110, 940), "y"),
			visualControl("o3", rectT(120, 900, 170, 940), "z"),
		}
		requireVisualHint(t, visualTree(1000, 1000, children...), "com.example.app",
			VisualKindImageHeavy, "image_heavy(50%)")
	})

	t.Run("text-rich page is never image-heavy", func(t *testing.T) {
		page := func(runes int) *protocol.Tree {
			return visualTree(1000, 1000,
				visualPicture("n2", rectT(0, 0, 1000, 500)),
				nodeT("n3", protocol.RoleText, rectT(0, 600, 1000, 700), strings.Repeat("字", runes)))
		}
		requireVisualHint(t, page(149), "com.example.app",
			VisualKindImageHeavy, "image_heavy(50%)")
		requireVisualHint(t, page(150), "com.example.app", "", "")
	})

	t.Run("only the largest sixty-four pictures are unioned", func(t *testing.T) {
		// Sixty-four identical covers of 44.9% plus one smaller 5% strip below
		// them. The union of the largest 64 stays at 44.9% and misses the 45%
		// threshold; without the sample cap the strip would push it to 49.9%.
		children := make([]*protocol.Node, 0, 70)
		for i := 0; i < 64; i++ {
			children = append(children, visualPicture(
				"i"+itoa(i+1), rectT(0, 0, 1000, 449)))
		}
		children = append(children, visualPicture("s1", rectT(0, 449, 1000, 499)))
		children = append(children, visualControlBar()...)
		requireVisualHint(t, visualTree(1000, 1000, children...), "com.example.app", "", "")
	})

	t.Run("percentage rounds to the nearest point", func(t *testing.T) {
		low := visualTree(1000, 1000, visualPicture("n2", rectT(0, 0, 454, 1000)))
		requireVisualHint(t, low, "com.example.app", VisualKindImageHeavy, "image_heavy(45%)")
		high := visualTree(1000, 1000, visualPicture("n2", rectT(0, 0, 455, 1000)))
		requireVisualHint(t, high, "com.example.app", VisualKindImageHeavy, "image_heavy(46%)")
	})
}

func TestInferVisualHintSparseTree(t *testing.T) {
	t.Run("video page without pictures", func(t *testing.T) {
		tree := visualTree(1000, 1000,
			visualControl("n2", rectT(20, 900, 200, 980), "播放视频"),
			visualControl("n3", rectT(800, 900, 980, 980), "全屏观看"),
			nodeT("n4", protocol.RoleText, rectT(400, 900, 600, 940), "进度"),
		)
		requireVisualHint(t, tree, "com.example.app", VisualKindSparseTree, "sparse_tree")
	})

	t.Run("blank launcher is never sparse", func(t *testing.T) {
		empty := visualTree(1000, 1000)
		requireVisualHint(t, empty, device.ForegroundNone, "", "")
		requireVisualHint(t, empty, "com.example.app", VisualKindSparseTree, "sparse_tree")
	})

	t.Run("thresholds are strict", func(t *testing.T) {
		text := func(runes int) *protocol.Tree {
			return visualTree(1000, 1000,
				nodeT("n2", protocol.RoleText, rectT(0, 0, 500, 40), strings.Repeat("字", runes)))
		}
		requireVisualHint(t, text(19), "com.example.app", VisualKindSparseTree, "sparse_tree")
		requireVisualHint(t, text(20), "com.example.app", "", "")

		controls := func(count int) *protocol.Tree {
			children := make([]*protocol.Node, 0, count)
			for i := 0; i < count; i++ {
				children = append(children, visualControl(
					"c"+itoa(i+1), rectT(i*40, 900, i*40+30, 940), "c"))
			}
			return visualTree(1000, 1000, children...)
		}
		requireVisualHint(t, controls(4), "com.example.app", VisualKindSparseTree, "sparse_tree")
		requireVisualHint(t, controls(5), "com.example.app", "", "")
	})

	t.Run("covered composite text is counted once", func(t *testing.T) {
		// The parent button carries fifteen runes; the child text repeats them.
		// Only the uncovered part counts, so the page stays sparse.
		parent := visualControl("n2", rectT(0, 0, 600, 200), "一二三四五六七八九十甲乙丙丁戊")
		parent.Children = []*protocol.Node{
			nodeT("n3", protocol.RoleText, rectT(0, 0, 600, 100), "一二三四五六七八九十甲乙丙丁戊"),
		}
		requireVisualHint(t, visualTree(1000, 1000, parent), "com.example.app",
			VisualKindSparseTree, "sparse_tree")
	})

	t.Run("context, protected value and whitespace are excluded", func(t *testing.T) {
		contextNode := nodeT("n2", protocol.RoleGroup, nil, "很长的上下文说明文字内容")
		contextNode.Context = true
		protected := plainT("n3", protocol.RoleInput)
		protected.Value = readString("秘密的值不会公开")
		protected.Protected = readBool(true)
		spaces := nodeT("n4", protocol.RoleText, rectT(0, 0, 100, 20), "   \t  ")
		requireVisualHint(t, visualTree(1000, 1000, contextNode, protected, spaces),
			"com.example.app", VisualKindSparseTree, "sparse_tree")

		// A published hint is readable content and is counted.
		hinted := plainT("n5", protocol.RoleGroup)
		hinted.Hint = readString(strings.Repeat("字", 20))
		requireVisualHint(t, visualTree(1000, 1000, hinted), "com.example.app", "", "")
	})

	t.Run("pictures win over the sparse branch", func(t *testing.T) {
		// A picture-heavy page with almost no text is image_heavy, not sparse.
		tree := visualTree(1000, 1000, visualPicture("n2", rectT(0, 0, 1000, 520)))
		requireVisualHint(t, tree, "com.example.app", VisualKindImageHeavy, "image_heavy(52%)")
	})
}

func TestInferVisualHintIsStableAcrossIdenticalFrames(t *testing.T) {
	build := func() *protocol.Tree {
		return visualTree(1000, 1000,
			visualPicture("n2", rectT(0, 0, 1000, 520)),
			nodeT("n3", protocol.RoleText, rectT(0, 600, 1000, 700), "画廊标题"))
	}
	firstKind, firstValue := InferVisualHint(build(), "com.example.gallery")
	secondKind, secondValue := InferVisualHint(build(), "com.example.gallery")
	if firstKind != secondKind || firstValue != secondValue || firstKind == "" {
		t.Fatalf("identical frames produced different hints: (%q,%q) vs (%q,%q)",
			firstKind, firstValue, secondKind, secondValue)
	}
}

func TestUnionRectArea(t *testing.T) {
	cases := []struct {
		name  string
		rects []protocol.Bounds
		want  int64
	}{
		{"empty", nil, 0},
		{"single", []protocol.Bounds{{Left: 0, Top: 0, Right: 10, Bottom: 10}}, 100},
		{
			"disjoint",
			[]protocol.Bounds{{Left: 0, Top: 0, Right: 10, Bottom: 10},
				{Left: 20, Top: 0, Right: 30, Bottom: 10}},
			200,
		},
		{
			"adjacent_edges",
			[]protocol.Bounds{{Left: 0, Top: 0, Right: 10, Bottom: 10},
				{Left: 10, Top: 0, Right: 20, Bottom: 10}},
			200,
		},
		{
			"half_overlap",
			[]protocol.Bounds{{Left: 0, Top: 0, Right: 10, Bottom: 10},
				{Left: 5, Top: 5, Right: 15, Bottom: 15}},
			175,
		},
		{
			"contained",
			[]protocol.Bounds{{Left: 0, Top: 0, Right: 10, Bottom: 10},
				{Left: 2, Top: 2, Right: 4, Bottom: 4}},
			100,
		},
		{
			"identical",
			[]protocol.Bounds{{Left: 0, Top: 0, Right: 10, Bottom: 10},
				{Left: 0, Top: 0, Right: 10, Bottom: 10}},
			100,
		},
		{
			"cross",
			[]protocol.Bounds{{Left: 0, Top: 4, Right: 10, Bottom: 6},
				{Left: 4, Top: 0, Right: 6, Bottom: 10}},
			20 + 20 - 4,
		},
		{
			"gap_between_rows",
			[]protocol.Bounds{{Left: 0, Top: 0, Right: 10, Bottom: 4},
				{Left: 0, Top: 6, Right: 10, Bottom: 10}},
			80,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]visualRect, 0, len(tc.rects))
			for _, rect := range tc.rects {
				items = append(items, newVisualRect(rect))
			}
			if got := unionRectArea(items); got != tc.want {
				t.Fatalf("unionRectArea = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestInferVisualHintOnHugeScreenStaysExact(t *testing.T) {
	// A screen whose pixel area (2^60) is far above the range where area*100 fits
	// in int64: the area comparisons and the published percentage still have to be
	// exact.
	const side = int64(1) << 30
	width := int(side)
	tree := visualTree(width, width,
		visualPicture("n2", rectT(0, 0, width/2, width)),
		visualPicture("n3", rectT(width/4, 0, width*3/4, width)))
	requireVisualHint(t, tree, "com.example.app", VisualKindImageHeavy, "image_heavy(75%)")
}

func TestVisualPercentHelpersDoNotOverflow(t *testing.T) {
	// A screen whose pixel area is near the int64 limit still yields an exact
	// comparison and a bounded percentage.
	const huge = int64(1) << 62
	if !areaAtLeastPercent(huge, huge, 45) {
		t.Fatal("100% must reach the 45% threshold")
	}
	if areaAtLeastPercent(huge/10, huge, 45) {
		t.Fatal("10% must not reach the 45% threshold")
	}
	if got := roundedPercentOf(huge, huge); got != 100 {
		t.Fatalf("roundedPercentOf(100%%) = %d, want 100", got)
	}
	if got := roundedPercentOf(huge/2, huge); got != 50 {
		t.Fatalf("roundedPercentOf(50%%) = %d, want 50", got)
	}
	if got := roundedPercentOf(huge/100, huge); got != 1 {
		t.Fatalf("roundedPercentOf(1%%) = %d, want 1", got)
	}
	if got := roundedPercentOf(huge/1000, huge); got != 0 {
		t.Fatalf("roundedPercentOf(0.1%%) = %d, want 0", got)
	}
}
