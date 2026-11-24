package main

import (
	"encoding/json/v2"
	"strconv"
	"strings"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
	"github.com/malyjacob/mobile-use-vnext/internal/treetext"
)

// checkProjectionConsistency applies the two projection gates that need both
// channels at once: the readable tree body must be exactly the canonical
// projection of the structured tree, and the optional visual classification member
// must be one the frozen grammar allows on a frame that actually carries a tree.
// It runs on the already-decoded result, so every tool call and every result mode
// shares it.
func checkProjectionConsistency(projected *callResult) *checkError {
	if failure := checkObservationVisual(projected); failure != nil {
		return failure
	}
	return checkTreeBodyAgainstStructured(projected)
}

// checkObservationVisual validates the optional observation.visual member. The
// member is optional, but when the projection carries it the value must be one the
// engine can compute - the sparse classification or the clamped whole-page image
// share - and only a frame with a tree has a page classification to report. A
// present member that is not a string (including an explicit null) is not what the
// structured contract declares, so it is rejected instead of being read as absent.
func checkObservationVisual(projected *callResult) *checkError {
	if projected == nil || projected.object == nil {
		return nil
	}
	observationObject, ok := projected.object["observation"].(map[string]any)
	if !ok || observationObject == nil {
		return nil
	}
	rawVisual, present := observationObject["visual"]
	if !present {
		return nil
	}
	value, isString := rawVisual.(string)
	if !isString {
		return failf("observation_visual_unexpected")
	}
	if !validVisualHintToken(value) {
		return failf("observation_visual_invalid")
	}
	if !observationCarriesTree(observationObject) {
		return failf("observation_visual_without_tree")
	}
	return nil
}

// observationCarriesTree reports whether the observation object carries a tree of
// one of the two readable formats. A null tree, a missing tree and an unreadable
// format all mean "no page was observed".
func observationCarriesTree(observation map[string]any) bool {
	tree, ok := observation["tree"].(map[string]any)
	if !ok || tree == nil {
		return false
	}
	format, _ := tree["format"].(string)
	return format == treeFull || format == treeDelta
}

// checkTreeBodyAgainstStructured re-renders the structured compact tree into its
// canonical tree v5 block and compares the delivered body line by line. It is the
// lossless half of the three-mode contract: the readable block is a projection of
// the same tree, so the two channels may not disagree about a label, a delivered
// geometry, an action or a delta member. The independent grammar validation has
// already accepted the block; this gate only compares its content.
//
// A delivered block is the claim that the structured tree round-tripped losslessly,
// so the gate fails closed: once a block exists, a structured tree that cannot be
// re-rendered (a header-only tree, an illegal ref, a non-compact view) is the exact
// drift it exists to reject. Only a result without a readable block is exempt - the
// documented JSON fallback, whose header facts the object gate already compares. The
// historical anonymous chain shorthand is normalized line by line instead of
// exempting the whole body.
func checkTreeBodyAgainstStructured(projected *callResult) *checkError {
	if projected == nil || projected.textOnly || len(projected.texts) != 1 ||
		len(projected.structured) == 0 || projected.object == nil {
		return nil
	}
	actualBlock, format, found := findV5TreeBlock(projected.texts[0])
	if !found {
		// No readable block: the JSON fallback is verified by the object comparison.
		return nil
	}
	observationObject, _ := projected.object["observation"].(map[string]any)
	if observationObject == nil {
		return failUnrenderableTreeBody(format, "tree_missing")
	}
	treeObject, _ := observationObject["tree"].(map[string]any)
	if treeObject == nil {
		return failUnrenderableTreeBody(format, "tree_missing")
	}
	if treeFormat, _ := treeObject["format"].(string); treeFormat != format {
		return failUnrenderableTreeBody(format, "format_mismatch")
	}
	observationID, _ := observationObject["id"].(string)
	if !validTreeTextO(observationID) {
		return failUnrenderableTreeBody(format, "id_invalid")
	}
	encoded, err := json.Marshal(treeObject)
	if err != nil {
		return failUnrenderableTreeBody(format, "decode_invalid")
	}
	canonical, cause := canonicalTreeBlock(observationID, format, encoded)
	if cause != "" {
		return failUnrenderableTreeBody(format, cause)
	}
	actualBody := normalizeLegacyShorthand(treeBlockBody(actualBlock))
	canonicalBody := treeBlockBody(canonical)
	if len(actualBody) != len(canonicalBody) {
		return failf("both_mode_tree_body_mismatch",
			field("tree_format", format),
			field("count", "lines"),
			field("text", strconv.Itoa(len(actualBody))),
			field("structured", strconv.Itoa(len(canonicalBody))))
	}
	for index := range canonicalBody {
		if actualBody[index] != canonicalBody[index] {
			return failf("both_mode_tree_body_mismatch",
				field("tree_format", format),
				field("line", strconv.Itoa(index+1)))
		}
	}
	return nil
}

// failUnrenderableTreeBody is the closed failure of a delivered block whose
// structured tree cannot produce a canonical body. The cause is a controlled enum;
// no tree content is attached.
func failUnrenderableTreeBody(format, cause string) *checkError {
	return failf("both_mode_tree_body_unrenderable",
		field("tree_format", format), field("cause", cause))
}

// findV5TreeBlock returns the first complete tree v5 block of one readable
// projection and the format its header declares. A header without its trailer is
// not a complete block; the grammar gate already reports that truncation.
func findV5TreeBlock(text string) (string, string, bool) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for start, line := range lines {
		format := ""
		switch {
		case strings.HasPrefix(line, "tree v5 full nodes="):
			format = treeFull
		case strings.HasPrefix(line, "tree v5 delta base="):
			format = treeDelta
		default:
			continue
		}
		for finish := start + 1; finish < len(lines); finish++ {
			if lines[finish] == "end tree" {
				return strings.Join(lines[start:finish+1], "\n") + "\n", format, true
			}
		}
		return "", format, false
	}
	return "", "", false
}

// canonicalTreeBlock renders the canonical tree v5 block of one structured compact
// tree and reports the controlled cause when no canonical body exists.
func canonicalTreeBlock(observationID, format string, encoded []byte) (string, string) {
	switch format {
	case treeFull:
		var tree protocol.ReadTree
		if err := protocol.Decode(encoded, &tree); err != nil {
			return "", "decode_invalid"
		}
		block, ok := treetext.EncodeFull(observationID, &tree)
		if !ok {
			return "", "not_renderable"
		}
		return block, ""
	case treeDelta:
		var delta protocol.ReadDelta
		if err := protocol.Decode(encoded, &delta); err != nil {
			return "", "decode_invalid"
		}
		block, ok := treetext.EncodeDelta(observationID, &delta)
		if !ok {
			return "", "not_renderable"
		}
		return block, ""
	}
	return "", "format_invalid"
}

// treeBlockBody returns the definition lines of one complete tree block: the lines
// between the blank separator and the "end tree" trailer.
func treeBlockBody(block string) []string {
	lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	if len(lines) < 3 || lines[len(lines)-1] != "end tree" || lines[1] != "" {
		return nil
	}
	return lines[2 : len(lines)-1]
}

// normalizeLegacyShorthand rewrites the historical anonymous default-view chain
// spelling "[n2 n3]" into the explicit chain form the canonical encoder produces, so
// one legacy line is compared like every other line instead of exempting the whole
// body.
func normalizeLegacyShorthand(body []string) []string {
	normalized := make([]string, len(body))
	for index, line := range body {
		trimmed := strings.TrimLeft(line, " ")
		if len(trimmed) > 2 && strings.HasPrefix(trimmed, "[") &&
			strings.HasSuffix(trimmed, "]") {
			indent := line[:len(line)-len(trimmed)]
			normalized[index] = indent + "chain " + trimmed + " view"
			continue
		}
		normalized[index] = line
	}
	return normalized
}
