package protocol

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

// TestReadNodeTruncatedWireOrderAndDefault pins the compact-only truncation
// flag: it is written only when true, its column sits after resource and before
// context, and every other encoding omits it. Reverse control: writing the flag
// unconditionally or placing it in another column breaks the exact positions.
func TestReadNodeTruncatedWireOrderAndDefault(t *testing.T) {
	resource := "app:id/btn"
	node := &ReadNode{
		Ref: "n2", Role: RoleButton,
		Bounds:   KnownBounds(Bounds{Left: 10, Top: 20, Right: 110, Bottom: 220}),
		Label:    []string{"标题"},
		Actions:  []Action{ActionTap},
		Resource: &resource, Truncated: true, Context: true,
	}
	wire := string(marshal(t, node))
	resourceAt := strings.Index(wire, `"resource":"app:id/btn"`)
	truncatedAt := strings.Index(wire, `"truncated":true`)
	contextAt := strings.Index(wire, `"context":true`)
	if resourceAt < 0 || truncatedAt < 0 || contextAt < 0 ||
		!(resourceAt < truncatedAt && truncatedAt < contextAt) {
		t.Fatalf("truncated column order: %s", wire)
	}

	node.Truncated = false
	if string(marshal(t, node)) != strings.Replace(wire, `,"truncated":true`, "", 1) {
		t.Fatalf("false truncated must be omitted: %s", wire)
	}

	var decoded ReadNode
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.Truncated {
		t.Fatalf("truncated=true lost: %#v", decoded)
	}
	if clone := decoded.Clone(); !clone.Truncated {
		t.Fatalf("clone lost truncated: %#v", clone)
	}
}

// TestReadNodeTruncatedWireDecodeMatrix rejects explicit null and non-boolean
// values while keeping absent and explicit false as the known-false fact.
func TestReadNodeTruncatedWireDecodeMatrix(t *testing.T) {
	base := `{"ref":"n2","role":"button"`
	cases := []struct {
		name string
		body string
		ok   bool
		want bool
	}{
		{"absent", base + `}`, true, false},
		{"explicit false", base + `,"truncated":false}`, true, false},
		{"explicit true", `{"ref":"n2","role":"button","truncated":true}`, true, true},
		{"null", base + `,"truncated":null}`, false, false},
		{"string", base + `,"truncated":"true"}`, false, false},
		{"number", base + `,"truncated":1}`, false, false},
		{"array", base + `,"truncated":[]}`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var node ReadNode
			err := json.Unmarshal([]byte(tc.body), &node)
			if tc.ok && err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("accepted invalid truncated value: %s", tc.body)
				}
				return
			}
			if node.Truncated != tc.want {
				t.Fatalf("truncated = %v, want %v", node.Truncated, tc.want)
			}
		})
	}
}

// TestReadTreeValidateTruncatedViewRules keeps truncated labels out of the
// detailed view and out of the compact root and context positions.
func TestReadTreeValidateTruncatedViewRules(t *testing.T) {
	build := func(view string, rootTruncated, childContext, childTruncated bool) *ReadTree {
		root := testReadNode("n1", RoleRoot)
		root.Truncated = rootTruncated
		child := testReadNode("n2", RoleView)
		child.Bounds = HiddenBounds()
		child.Context = childContext
		child.Truncated = childTruncated
		root.Children = []*ReadNode{child}
		return &ReadTree{Format: "full", Scope: Scope, View: view, Root: root}
	}
	cases := []struct {
		name string
		tree *ReadTree
		ok   bool
	}{
		{"compact actionable", build(ViewCompact, false, false, true), true},
		{"compact known false", build(ViewCompact, false, false, false), true},
		{"detailed truncated", build(ViewDetailed, false, false, true), false},
		{"compact root truncated", build(ViewCompact, true, false, false), false},
		{"compact context truncated", build(ViewCompact, false, true, true), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.tree.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}

	// The same tree must survive the wire round trip.
	tree := build(ViewCompact, false, false, true)
	wire := string(marshal(t, tree))
	var decoded ReadTree
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatalf("decode compact tree: %v", err)
	}
	if !decoded.Root.Children[0].Truncated {
		t.Fatalf("tree round trip lost truncated: %s", wire)
	}
}

// TestReadSetNodeTruncatedThreeStates keeps the delta set member's omission,
// explicit false and explicit true distinct, and rejects null outright.
func TestReadSetNodeTruncatedThreeStates(t *testing.T) {
	build := func(truncated Field[bool]) *ReadDelta {
		return &ReadDelta{
			Format: "delta", Base: "O1", View: ViewCompact,
			Set: map[string]ReadSetNode{"n2": {Truncated: truncated}},
		}
	}
	off := build(Field[bool]{Set: true, Value: false})
	on := build(Field[bool]{Set: true, Value: true})
	if wire := string(marshal(t, off)); !strings.Contains(wire, `"truncated":false`) {
		t.Fatalf("explicit false lost: %s", wire)
	}
	if wire := string(marshal(t, on)); !strings.Contains(wire, `"truncated":true`) {
		t.Fatalf("explicit true lost: %s", wire)
	}
	if (ReadSetNode{Truncated: Field[bool]{Set: true, Value: false}}).IsZero() {
		t.Fatal("set with truncated=false reports zero")
	}

	var decoded ReadDelta
	if err := json.Unmarshal(marshal(t, on), &decoded); err != nil {
		t.Fatalf("decode set: %v", err)
	}
	if got := decoded.Set["n2"].Truncated; !got.Set || !got.Value {
		t.Fatalf("truncated=true lost: %#v", got)
	}
	decoded = ReadDelta{}
	if err := json.Unmarshal(marshal(t, off), &decoded); err != nil {
		t.Fatalf("decode set: %v", err)
	}
	if got := decoded.Set["n2"].Truncated; !got.Set || got.Value {
		t.Fatalf("truncated=false lost: %#v", got)
	}

	base := `{"format":"delta","base":"O1","view":"compact","set":{"n2":{%s}}}`
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"null", `"truncated":null`, false},
		{"string", `"truncated":"true"`, false},
		{"number", `"truncated":1`, false},
		{"unknown member", `"truncated":true,"unknown":1`, false},
		{"duplicate member", `"truncated":true,"truncated":false`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(base, "%s", tc.body, 1)
			var delta ReadDelta
			err := json.Unmarshal([]byte(body), &delta)
			if (err == nil) != tc.ok {
				t.Fatalf("Unmarshal() = %v, want ok=%v: %s", err, tc.ok, body)
			}
		})
	}
}

// TestReadDeltaValidateTruncatedDefinitions applies the compact position rule
// to add and replace definition subtrees.
func TestReadDeltaValidateTruncatedDefinitions(t *testing.T) {
	actionable := func(ref string, context bool, truncated bool) *ReadNode {
		node := testReadNode(ref, RoleButton)
		node.Context = context
		node.Truncated = truncated
		return node
	}
	cases := []struct {
		name  string
		delta *ReadDelta
		ok    bool
	}{
		{
			name: "add actionable truncated",
			delta: &ReadDelta{Format: "delta", Base: "O1", View: ViewCompact,
				Add: []*ReadNode{actionable("n2", false, true)}},
			ok: true,
		},
		{
			name: "add context truncated",
			delta: &ReadDelta{Format: "delta", Base: "O1", View: ViewCompact,
				Add: []*ReadNode{actionable("n2", true, true)}},
			ok: false,
		},
		{
			name: "replace context truncated",
			delta: &ReadDelta{Format: "delta", Base: "O1", View: ViewCompact,
				Replace: map[string]*ReadNode{"n2": actionable("n2", true, true)}},
			ok: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.delta.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}

	delta := &ReadDelta{Format: "delta", Base: "O1", View: ViewCompact,
		Add: []*ReadNode{actionable("n2", false, true)}}
	var decoded ReadDelta
	if err := json.Unmarshal(marshal(t, delta), &decoded); err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	if !decoded.Add[0].Truncated {
		t.Fatal("add definition lost truncated")
	}
}
