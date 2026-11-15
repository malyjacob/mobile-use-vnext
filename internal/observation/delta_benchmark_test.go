package observation

import (
	"fmt"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

var benchmarkDeltaSink *protocol.ReadDelta

func benchmarkDeltaTree(size int) *protocol.ReadTree {
	leaves := make([]*protocol.ReadNode, 0, size-2)
	for i := 0; i < size-2; i++ {
		leaves = append(leaves, readR(
			fmt.Sprintf("n%d", i+3),
			protocol.RoleText,
			fmt.Sprintf("row-%d", i),
		))
	}
	return readRootR(readR("n2", protocol.RoleScroll, "content", leaves...))
}

func benchmarkDeltaPair(size int, scenario string) (*protocol.ReadTree, *protocol.ReadTree) {
	base := benchmarkDeltaTree(size)
	current := benchmarkDeltaTree(size)
	scroll := current.Root.Children[0]
	switch scenario {
	case "sparse_updates":
		updates := 16
		if updates > len(scroll.Children)/8 {
			updates = len(scroll.Children) / 8
		}
		if updates < 1 {
			updates = 1
		}
		stride := len(scroll.Children) / updates
		changed := 0
		for i := 0; i < len(scroll.Children) && changed < updates; i += stride {
			scroll.Children[i].Label = []string{fmt.Sprintf("updated-%d", i)}
			changed++
		}
	case "single_property":
		index := len(scroll.Children) / 2
		scroll.Children[index].Label = []string{"single-update"}
	case "subtree_reparent":
		wrapperRef := fmt.Sprintf("n%d", size+100_000)
		wrapper := readR(wrapperRef, protocol.RoleGroup, "wrapper", scroll)
		current.Root.Children = []*protocol.ReadNode{wrapper}
	case "unchanged":
	default:
		panic("unknown benchmark scenario")
	}
	return base, PackTree(current)
}

func BenchmarkStructuralDeltaB03(b *testing.B) {
	for _, size := range []int{32, 256, 2048, 20000} {
		for _, scenario := range []string{
			"sparse_updates",
			"single_property",
			"subtree_reparent",
			"unchanged",
		} {
			name := fmt.Sprintf("%s/%d", scenario, size)
			b.Run(name, func(b *testing.B) {
				base, current := benchmarkDeltaPair(size, scenario)
				fullBytes, err := protocol.JSON(current)
				if err != nil {
					b.Fatalf("encode full snapshot: %v", err)
				}
				delta, err := ReadDiff("O1", base, current)
				if err != nil {
					b.Fatalf("ReadDiff: %v", err)
				}
				deltaBytes, err := protocol.JSON(delta)
				if err != nil {
					b.Fatalf("encode delta: %v", err)
				}
				if len(deltaBytes)*10 >= len(fullBytes)*7 {
					b.Fatalf("delta is not below the 70%% selection boundary: delta=%d full=%d",
						len(deltaBytes), len(fullBytes))
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchmarkDeltaSink, err = ReadDiff("O1", base, current)
					if err != nil {
						b.Fatalf("ReadDiff: %v", err)
					}
				}
				b.ReportMetric(float64(len(deltaBytes))*100/float64(len(fullBytes)), "delta/full-%")
				b.ReportMetric(float64(len(deltaBytes)), "delta-bytes")
				b.ReportMetric(float64(len(fullBytes)), "full-bytes")
			})
		}
	}
}
