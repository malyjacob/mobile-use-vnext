package observation

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The source purification tests cover the fixed Stage 1 rule: public readable
// labels lose the closed set of invisible placeholder code points, while the raw
// capture and every input value keep their original bytes.

func TestCleanReadableText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"ordinary_text_untouched", "个人闲置", "个人闲置"},
		{"ordinary_whitespace_trimmed", "  a \n b  ", "a \n b"},
		{"leading_newline_trimmed", "\nabc", "abc"},
		{"nbsp_only", "\u00a0", ""},
		{"newline_and_space_only", "\n ", ""},
		{"mixed_edge_whitespace", "\u00a0林俊杰 ", "林俊杰"},
		{"zero_width_between_han", "个\u200b人\u200b闲\u200b置\u200b", "个人闲置"},
		{"object_replacement_only", "\ufffc\ufffc", ""},
		{"closed_set_only", "\u200b\u200c\u200d\u2060\ufeff\ufffc", ""},
		{"mixed_placeholders_trim", "\u200b a\ufffc b \u2060", "a b"},
		{"blank_lines_untouched_without_placeholder", "a\n\nb", "a\n\nb"},
		{"blank_line_collapse", "a\n\n\u200b\nb", "a\nb"},
		{"blank_line_residue_collapse", "a\n \n\t\u200b\nb", "a\nb"},
		{"blank_line_with_placeholder", "a\n\u200b\nb", "a\nb"},
		{"single_newline_kept", "a\nb", "a\nb"},
		{"other_invisible_kept", "a\u00a0b", "a\u00a0b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanReadableText(tc.in); got != tc.want {
				t.Fatalf("cleanReadableText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildPurifiesReadableLabels(t *testing.T) {
	builder := newCap(1080, 2400)
	rootIdx := builder.root()

	// A leaf whose whole text is an object replacement placeholder has no
	// remaining semantics and must not enter the public tree at all.
	placeholder := defaultEmptyRaw(rect(0, 0, 200, 40), "android.widget.TextView")
	placeholder.Text = "\ufffc\ufffc"
	placeholderIdx := builder.add(rootIdx, placeholder)

	zeroWidthOnly := defaultEmptyRaw(rect(0, 50, 200, 90), "android.widget.TextView")
	zeroWidthOnly.Text = "\u200b"
	zeroWidthIdx := builder.add(rootIdx, zeroWidthOnly)

	product := defaultEmptyRaw(rect(0, 100, 400, 180), "android.widget.TextView")
	product.Text = "个\u200b人\u200b闲\u200b置\u200b"
	productIdx := builder.add(rootIdx, product)

	described := defaultEmptyRaw(rect(0, 200, 400, 280), "android.view.View")
	described.Description = "我的\u200b收藏"
	describedIdx := builder.add(rootIdx, described)

	// A description that differs only by edge whitespace and placeholders
	// shares the same cleaned label as the text.
	sameText := defaultEmptyRaw(rect(0, 300, 400, 360), "android.widget.TextView")
	sameText.Text = "消息\u200b"
	sameText.Description = "\u00a0消息 "
	sameTextIdx := builder.add(rootIdx, sameText)

	input := defaultEmptyRaw(rect(0, 400, 400, 460), "android.widget.EditText")
	input.Editable = true
	input.Text = " ab\u200bcd "
	input.Description = "搜索\u200b框"
	inputIdx := builder.add(rootIdx, input)

	trimmed := defaultEmptyRaw(rect(0, 600, 400, 660), "android.widget.TextView")
	trimmed.Text = "\u00a0林俊杰 "
	trimmed.Description = "\n "
	trimmedIdx := builder.add(rootIdx, trimmed)

	multiline := defaultEmptyRaw(rect(0, 700, 400, 760), "android.widget.TextView")
	multiline.Text = "a\n\nb"
	multilineIdx := builder.add(rootIdx, multiline)

	// A placeholder-only label on a control keeps the control but publishes no
	// label for it.
	placeholderButton := clickable(defaultEmptyRaw(rect(0, 500, 400, 560), "android.view.View"))
	placeholderButton.Text = "\uFFFC"
	placeholderButtonIdx := builder.add(rootIdx, placeholderButton)

	snap := buildOne(t, NewBuilder(), builder.capture())

	// The raw capture is untouched: revalidation and text read-back rely on it.
	if got := snap.Capture.Nodes[placeholderIdx].Text; got != "\ufffc\ufffc" {
		t.Fatalf("raw placeholder text changed: %q", got)
	}
	if got := snap.Capture.Nodes[productIdx].Text; got != "个\u200b人\u200b闲\u200b置\u200b" {
		t.Fatalf("raw product text changed: %q", got)
	}

	// Placeholder-only leaves never become public units.
	for ref, rawIdx := range snap.Source {
		if rawIdx == placeholderIdx || rawIdx == zeroWidthIdx {
			t.Fatalf("placeholder-only leaf published as %q", ref)
		}
	}
	if got := snap.Capture.Nodes[sameTextIdx].Description; got != "\u00a0消息 " {
		t.Fatalf("raw description changed: %q", got)
	}
	if got := snap.Capture.Nodes[inputIdx].Text; got != " ab\u200bcd " {
		t.Fatalf("raw input text changed: %q", got)
	}
	if len(snap.Tree.Root.Children) != 7 {
		t.Fatalf("public root children = %d, want 7\n%s",
			len(snap.Tree.Root.Children), dump(snap.Tree))
	}

	productNode := publicNodeAt(t, snap, productIdx)
	if len(productNode.Label) != 1 || productNode.Label[0] != "个人闲置" {
		t.Fatalf("product label = %v, want [个人闲置]", productNode.Label)
	}
	// The literal query an operator or model uses now matches the public label.
	if !strings.Contains(productNode.Label[0], "个人闲置") {
		t.Fatalf("purified label does not answer the literal query: %q", productNode.Label[0])
	}

	describedNode := publicNodeAt(t, snap, describedIdx)
	if len(describedNode.Label) != 1 || describedNode.Label[0] != "我的收藏" {
		t.Fatalf("description label = %v, want [我的收藏]", describedNode.Label)
	}

	sameTextNode := publicNodeAt(t, snap, sameTextIdx)
	if len(sameTextNode.Label) != 1 || sameTextNode.Label[0] != "消息" {
		t.Fatalf("deduplicated description label = %v, want [消息]", sameTextNode.Label)
	}

	inputNode := publicNodeAt(t, snap, inputIdx)
	if len(inputNode.Label) != 2 || inputNode.Label[0] != "abcd" || inputNode.Label[1] != "搜索框" {
		t.Fatalf("input label = %v, want [abcd 搜索框]", inputNode.Label)
	}
	// An input's value keeps the original bytes: it is written back verbatim.
	if inputNode.Value == nil || *inputNode.Value != " ab\u200bcd " {
		t.Fatalf("input value = %v, want the raw bytes", inputNode.Value)
	}

	if node := publicNodeAt(t, snap, trimmedIdx); !equalStrings(node.Label, []string{"林俊杰"}) {
		t.Fatalf("expected trimmed text and no whitespace-only description: %q", node.Label)
	}
	if node := publicNodeAt(t, snap, multilineIdx); !equalStrings(node.Label, []string{"a\n\nb"}) {
		t.Fatalf("expected interior line breaks to be preserved: %q", node.Label)
	}

	buttonNode := publicNodeAt(t, snap, placeholderButtonIdx)
	if buttonNode.Role != protocol.RoleView || len(buttonNode.Label) != 0 {
		t.Fatalf("placeholder-only clickable = %+v, want an unlabeled view", buttonNode)
	}
}

// publicNodeAt returns the published node that maps to one raw capture index.
func publicNodeAt(t *testing.T, snap *Snapshot, rawIdx int) *protocol.Node {
	t.Helper()
	for ref, index := range snap.Source {
		if index != rawIdx {
			continue
		}
		if node := find(snap.Tree, ref); node != nil {
			return node
		}
	}
	t.Fatalf("no public node maps to raw index %d", rawIdx)
	return nil
}

func TestSignatureAndPublicAnchorUseCleanedDescription(t *testing.T) {
	raw := defaultEmptyRaw(rect(0, 0, 400, 100), "android.widget.EditText")
	raw.Editable = true
	raw.Description = "搜索\u200b框"
	raw.DescSet = true

	if got := signature(&raw, protocol.RoleInput).anchor; got != "搜索框" {
		t.Fatalf("identity anchor = %q, want %q", got, "搜索框")
	}
	if got := publicSignature(&raw, protocol.RoleInput).anchor; got != "搜索框" {
		t.Fatalf("public anchor = %q, want %q", got, "搜索框")
	}

	// A password field never publishes its description anchor.
	raw.HasPassword, raw.Password = true, true
	if got := signature(&raw, protocol.RoleInput).anchor; got != "" {
		t.Fatalf("protected identity anchor = %q, want empty", got)
	}
	if got := publicSignature(&raw, protocol.RoleInput).anchor; got != "" {
		t.Fatalf("protected public anchor = %q, want empty", got)
	}
}

func TestPurifiedLabelsKeepRefContinuity(t *testing.T) {
	builder := newCap(1080, 2400)
	rootIdx := builder.root()
	product := defaultEmptyRaw(rect(0, 0, 400, 80), "android.widget.TextView")
	product.Text = "个\u200b人\u200b闲\u200b置\u200b"
	builder.add(rootIdx, product)

	b := NewBuilder()
	first := buildOne(t, b, builder.capture())
	// The same page with a different placeholder run is the same readable label,
	// so continuity keeps the very same ref instead of allocating a new one.
	builder.nodes[1].Text = "\u200b个\u200b人闲\u200b置\u200b"
	second := buildOne(t, b, builder.capture())
	if len(first.Refs) != len(second.Refs) || first.Refs[1] != second.Refs[1] {
		t.Fatalf("refs changed across placeholder edits: %v -> %v", first.Refs, second.Refs)
	}
	firstLabel := publicNodeAt(t, first, 1).Label
	secondLabel := publicNodeAt(t, second, 1).Label
	if strings.Join(firstLabel, "|") != strings.Join(secondLabel, "|") ||
		firstLabel[0] != "个人闲置" {
		t.Fatalf("labels differ across placeholder edits: %v vs %v", firstLabel, secondLabel)
	}
}
