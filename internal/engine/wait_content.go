package engine

import (
	"crypto/sha256"
	"encoding/json/v2"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// waitDigestOf selects the comparison used throughout a single wait.
func waitDigestOf(capture *device.Capture, tree *protocol.Tree, stable bool) ([32]byte, error) {
	if stable {
		return contentDigestOf(capture, tree)
	}
	digest, err := sampleDigestOf(capture, tree)
	if err != nil {
		return [32]byte{}, err
	}
	var layers [3 * sha256.Size]byte
	copy(layers[:sha256.Size], digest.hierarchy[:])
	copy(layers[sha256.Size:2*sha256.Size], digest.tree[:])
	copy(layers[2*sha256.Size:], digest.screen[:])
	return sha256.Sum256(layers[:]), nil
}

// contentDigestOf ignores geometry, ref assignment and text on passive leaves.
// The original full tree is still used for literal matching and publication.
func contentDigestOf(capture *device.Capture, tree *protocol.Tree) ([32]byte, error) {
	filtered := *tree
	filtered.Root = tree.Root.Clone()
	filterWaitContent(filtered.Root)
	encoded, err := json.Marshal(&filtered)
	if err != nil {
		return [32]byte{}, err
	}

	h := sha256.New()
	h.Write([]byte("wait-content"))
	screen := screenDigest(capture)
	h.Write(screen[:])
	h.Write(encoded)
	var digest [32]byte
	h.Sum(digest[:0])
	return digest, nil
}

func filterWaitContent(node *protocol.Node) {
	if node == nil {
		return
	}
	node.Bounds = nil
	node.Ref = ""
	if len(node.Children) == 0 && len(node.Actions) == 0 {
		node.Label = nil
		node.Value = nil
		node.Hint = nil
	}
	for _, child := range node.Children {
		filterWaitContent(child)
	}
}
