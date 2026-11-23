package treetext

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func testBoolPtr(value bool) *bool { return &value }

func testStringPtr(value string) *string { return &value }

func testBounds(left, top, right, bottom int) protocol.ReadBounds {
	return protocol.KnownBounds(protocol.Bounds{
		Left: left, Top: top, Right: right, Bottom: bottom,
	})
}

func testNode(
	ref string,
	role protocol.Role,
	bounds protocol.ReadBounds,
) *protocol.ReadNode {
	return &protocol.ReadNode{
		Ref: ref, Role: role, Bounds: bounds,
		Label:   []string{},
		Actions: []protocol.Action{},
		Enabled: testBoolPtr(true), Focused: testBoolPtr(false),
		Selected: testBoolPtr(false), Protected: testBoolPtr(false),
		Checked:  testBoolPtr(false),
		Children: []*protocol.ReadNode{},
	}
}

func testFullTree() *protocol.ReadTree {
	root := testNode("n1", protocol.RoleRoot, testBounds(0, 0, 1080, 2400))
	group := testNode("n2", protocol.RoleGroup, testBounds(0, 0, 1080, 220))

	back := testNode("n3", protocol.RoleButton, testBounds(24, 60, 144, 180))
	back.Label = []string{"返回"}
	back.Actions = []protocol.Action{protocol.ActionTap}

	title := testNode("n4", protocol.RoleText, testBounds(300, 80, 780, 160))
	title.Label = []string{"设置"}

	toggle := testNode("n6", protocol.RoleSwitch, testBounds(900, 60, 1032, 180))
	toggle.Label = []string{"启用"}
	toggle.Actions = []protocol.Action{protocol.ActionTap}
	toggle.Checked = testBoolPtr(true)

	scroll := testNode("n5", protocol.RoleScroll, testBounds(0, 220, 1080, 2400))
	scroll.Axis = protocol.AxisY

	group12 := testNode("n12", protocol.RoleGroup, protocol.UnknownBounds())
	search := testNode("n13", protocol.RoleInput, testBounds(48, 1850, 1032, 1990))
	search.Label = []string{"搜索设置"}
	search.Value = testStringPtr("")
	search.Hint = testStringPtr("输入关键词")
	search.Actions = []protocol.Action{protocol.ActionTap, protocol.ActionText}

	anonymous := testNode("n14", protocol.RoleGroup, protocol.HiddenBounds())
	anonymous.Checked = nil

	group.Children = []*protocol.ReadNode{back, title, toggle}
	group12.Children = []*protocol.ReadNode{search}
	scroll.Children = []*protocol.ReadNode{group12, anonymous}
	root.Children = []*protocol.ReadNode{group, scroll}
	return &protocol.ReadTree{
		Format: "full", Scope: protocol.Scope,
		View: protocol.ViewCompact, Root: root,
	}
}

const fullGolden = `tree v4 full nodes=9

n1 root 0,0,1080,2400
 n2 group 0,0,1080,220
  n3 button 24,60,144,180 "返回"
  n4 text 300,80,780,160 "设置"
  n6 switch 900,60,1032,180 "启用" tap checked=true
 n5 scroll 0,220,1080,2400 !swipe axis=y
  n12 group -
   n13 input 48,1850,1032,1990 "搜索设置" value="" hint="输入关键词" tap
  n14 group checked=null
end tree
`

func testDelta() *protocol.ReadDelta {
	add := testNode("n14", protocol.RoleGroup, testBounds(0, 460, 1080, 580))
	mobile := testNode("n15", protocol.RoleText, testBounds(48, 490, 700, 550))
	mobile.Label = []string{"移动网络"}
	mobile.Actions = []protocol.Action{protocol.ActionTap}
	add.Children = []*protocol.ReadNode{mobile}

	replace := testNode("n12", protocol.RoleGroup, testBounds(0, 1800, 1080, 2100))
	search := testNode("n91", protocol.RoleInput, testBounds(48, 1850, 1032, 1990))
	search.Label = []string{"搜索设置"}
	search.Value = testStringPtr("")
	search.Hint = testStringPtr("输入关键词")
	search.Actions = []protocol.Action{protocol.ActionTap, protocol.ActionText}
	replace.Children = []*protocol.ReadNode{search}

	return &protocol.ReadDelta{
		Format: "delta", Base: "O3", View: protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			"n8": {
				Checked: protocol.Field[*bool]{
					Set: true, Value: testBoolPtr(false),
				},
			},
			"n7": {
				Label: protocol.Field[[]string]{
					Set: true, Value: []string{"WLAN 已关闭"},
				},
				Actions: protocol.Field[[]protocol.Action]{
					Set: true, Value: []protocol.Action{protocol.ActionTap},
				},
			},
		},
		Add:      []*protocol.ReadNode{add},
		Remove:   []string{"n16"},
		Children: map[string][]string{"n5": {"n6", "n9", "n14"}},
		Replace:  map[string]*protocol.ReadNode{"n12": replace},
	}
}

const deltaGolden = `tree v4 delta base=O3 changes=6

~ n7 label=["WLAN 已关闭"] actions=["tap"]
~ n8 checked=false
+ n14 group 0,460,1080,580
 n15 text 48,490,700,550 "移动网络" tap
- n16
children n5 = [n6 n9 n14]
= n12 group 0,1800,1080,2100
 n91 input 48,1850,1032,1990 "搜索设置" value="" hint="输入关键词" tap
end tree
`

func TestEncodeFullGoldenBytes(t *testing.T) {
	block, ok := EncodeFull("O3", testFullTree())
	if !ok {
		t.Fatal("full tree is not encodable")
	}
	if block != fullGolden {
		t.Fatalf("full block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, fullGolden)
	}
}

func TestEncodeDeltaGoldenBytesAndShortBase(t *testing.T) {
	delta := testDelta()
	delta.Base = "O18446744073709551615_3"
	block, ok := EncodeDelta("O18446744073709551615_4", delta)
	if !ok {
		t.Fatal("delta is not encodable")
	}
	const wantHeader = "tree v4 delta base=_3 changes=6\n\n"
	if !strings.HasPrefix(block, wantHeader) {
		t.Fatalf("delta header = %q, want prefix %q", block, wantHeader)
	}
	withLongBase := deltaGolden
	withShortBase := strings.Replace(
		withLongBase,
		"tree v4 delta base=O3 changes=6\n\n",
		wantHeader,
		1,
	)
	if block != withShortBase {
		t.Fatalf("delta block mismatch:\n--- got ---\n%s\n--- want ---\n%s",
			block, withShortBase)
	}
}

func TestEncodeRejectsNonCompactOrInvalidInputs(t *testing.T) {
	full := testFullTree()
	full.View = protocol.ViewDetailed
	if block, ok := EncodeFull("O1", full); ok || block != "" {
		t.Fatalf("detailed full encoding = (%q, %t), want rejection", block, ok)
	}

	delta := testDelta()
	delta.Remove = []string{"n16", "n16"}
	if block, ok := EncodeDelta("O3", delta); ok || block != "" {
		t.Fatalf("duplicate remove encoding = (%q, %t), want rejection", block, ok)
	}
}
