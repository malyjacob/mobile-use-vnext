package observation

import (
	"fmt"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// TestStressPlanner guards the planner's time complexity and termination on
// pathological shapes. It is not a benchmark: the bounds are generous (the
// implemented planner takes single-digit milliseconds for each case) and only
// catch a regression to a quadratic or non-terminating walk.
func TestStressPlanner(t *testing.T) {
	cases := map[string]func() (*protocol.Tree, *protocol.Tree){
		// A 2000-level chain exercises parent-chain walks (region closure,
		// coverage): a per-node climb would be O(n^2).
		"deep": func() (*protocol.Tree, *protocol.Tree) {
			mk := func(suffix string) *protocol.Tree {
				root := &protocol.Node{Ref: "r", Role: protocol.RoleRoot}
				cur := root
				for i := 0; i < 2000; i++ {
					n := &protocol.Node{Ref: fmt.Sprintf("d%d", i), Role: protocol.RoleGroup}
					if i%2 == 0 {
						n.Label = []string{"x" + suffix}
					}
					cur.Children = []*protocol.Node{n}
					cur = n
				}
				return fullTree(root)
			}
			return mk(""), mk("2")
		},
		// 3000 siblings exercise the per-parent child list comparisons.
		"wide": func() (*protocol.Tree, *protocol.Tree) {
			mk := func(flip bool) *protocol.Tree {
				root := &protocol.Node{Ref: "r", Role: protocol.RoleRoot}
				for i := 0; i < 3000; i++ {
					n := &protocol.Node{Ref: fmt.Sprintf("w%d", i), Role: protocol.RoleText, Label: []string{fmt.Sprintf("t%d", i)}}
					if flip && i%3 == 0 {
						n.Label = []string{fmt.Sprintf("u%d", i)}
					}
					root.Children = append(root.Children, n)
				}
				return fullTree(root)
			}
			return mk(false), mk(true)
		},
		// A caller-built tree with a duplicated ref is malformed; planning must
		// terminate and either produce a verified delta or reject the pair.
		"duplicate ref": func() (*protocol.Tree, *protocol.Tree) {
			baseRoot := &protocol.Node{Ref: "r", Role: protocol.RoleRoot,
				Children: []*protocol.Node{{Ref: "a", Role: protocol.RoleText, Label: []string{"a"}}}}
			curRoot := &protocol.Node{Ref: "r", Role: protocol.RoleRoot,
				Children: []*protocol.Node{
					{Ref: "a", Role: protocol.RoleText, Label: []string{"a"}},
					{Ref: "a", Role: protocol.RoleText, Label: []string{"b"}},
				}}
			return fullTree(baseRoot), fullTree(curRoot)
		},
	}
	const budget = 10 * time.Second
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			base, cur := mk()
			start := time.Now()
			delta, err := Diff("O1", base, cur)
			elapsed := time.Since(start)
			if elapsed > budget {
				t.Fatalf("%s took %v (budget %v)", name, elapsed, budget)
			}
			if delta == nil && err == nil {
				t.Fatalf("%s: no delta and no error", name)
			}
		})
	}
}
