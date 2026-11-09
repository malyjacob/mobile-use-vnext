package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// pointState is the fixed result of resolving one logical display point in a
// public tree. The three values have distinct, pre-declared behaviors: a unique
// node can be proven, an ambiguous point is rejected before claim, and a point
// the tree does not describe keeps the foreground-only behavior.
type pointState string

const (
	pointUnique    pointState = "unique"
	pointAmbiguous pointState = "ambiguous"
	pointNone      pointState = "none"
)

// pointCheck is the immutable result of resolving a coordinate target in the
// observation the caller actually read. It is computed before claim and carries
// only the point, its resolution state, the resolved raw source index and the
// pure-CPU cost of the proof; it never carries a ref or tree content.
type pointCheck struct {
	x, y  int
	state pointState
	index int // raw index in obs.Projection.Capture.Nodes; -1 unless unique
	took  time.Duration
}

// coordinatePoint returns the point a coordinate target must prove. tap,
// long_press and text prove their target point; swipe proves only its start
// point because the end point is direction and magnitude, not node identity.
// Ref targets and key operations have no point to resolve.
func coordinatePoint(operation *protocol.Operation) (x, y int, ok bool) {
	if operation == nil {
		return 0, 0, false
	}
	switch operation.Type {
	case "tap", "long_press", "text":
		if operation.Target != nil && !operation.Target.UseRef {
			return operation.Target.X, operation.Target.Y, true
		}
	case "swipe":
		if operation.From != nil {
			return operation.From.X, operation.From.Y, true
		}
	}
	return 0, 0, false
}

// resolvePoint takes the deepest public node whose half-open bounds contain the
// point. Root and context nodes are not candidates; their descendants still
// are. Equally deep candidates cannot be ancestors of each other, so more than
// one means the point did not identify a node and the result is ambiguous.
func resolvePoint(tree *protocol.Node, x, y int) (string, pointState) {
	if tree == nil {
		return "", pointNone
	}
	bestRef := ""
	bestDepth := -1
	bestCount := 0
	var walk func(node *protocol.Node, depth int)
	walk = func(node *protocol.Node, depth int) {
		if node == nil {
			return
		}
		if depth > 0 && !node.Context && node.Role != protocol.RoleRoot &&
			node.Bounds != nil && node.Bounds.Contains(x, y) {
			switch {
			case depth > bestDepth:
				bestDepth, bestRef, bestCount = depth, node.Ref, 1
			case depth == bestDepth:
				bestCount++
			}
		}
		for _, child := range node.Children {
			walk(child, depth+1)
		}
	}
	walk(tree, 0)
	switch {
	case bestCount == 0:
		return "", pointNone
	case bestCount > 1:
		return "", pointAmbiguous
	default:
		return bestRef, pointUnique
	}
}

// resolveObservedPoint resolves the operation's coordinate target in the
// observation projection the caller read. Ambiguity is rejected here, before
// claim, capture or any device read; no node keeps the foreground-only path.
func resolveObservedPoint(obs *observationState, operation *protocol.Operation) (*pointCheck, *protocol.APIError) {
	if obs == nil || obs.Projection == nil || obs.Projection.Tree == nil ||
		obs.Projection.Capture == nil {
		return nil, nil
	}
	x, y, ok := coordinatePoint(operation)
	if !ok {
		return nil, nil
	}
	started := time.Now()
	ref, state := resolvePoint(obs.Projection.Tree.Root, x, y)
	check := &pointCheck{x: x, y: y, state: state, index: -1}
	if state == pointUnique {
		index, mapped := obs.Projection.Source[ref]
		if !mapped || index < 0 || index >= len(obs.Projection.Capture.Nodes) {
			// A published, non-root, non-context node always has a source
			// mapping. Should that invariant ever break, the point has no
			// provable node and keeps the foreground-only behavior instead of
			// rejecting a point the caller may still be able to use.
			check.state = pointNone
		} else {
			check.index = index
		}
	}
	check.took = time.Since(started)
	if check.state == pointAmbiguous {
		return check, protocol.NewError(422, protocol.CodeAmbiguousPoint,
			"坐标落在多个同级节点上，请改用 ref")
	}
	return check, nil
}

// indexOf returns the index of node in capture, or -1 when it is not a node of
// that capture. Identity matching alone never crosses captures by pointer.
func indexOf(capture *device.Capture, node *device.RawNode) int {
	if capture == nil || node == nil {
		return -1
	}
	for i := range capture.Nodes {
		if &capture.Nodes[i] == node {
			return i
		}
	}
	return -1
}
