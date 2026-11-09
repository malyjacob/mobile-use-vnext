package mcp

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	// maxObservationTreeTextBytes bounds one full/delta tree block, including
	// its header, blank separator and trailer. It is a fixed projection policy,
	// not a per-call option.
	maxObservationTreeTextBytes = 24 * 1024
	// maxInspectionTextBytes bounds one inspect block. Inspection is the cold
	// recovery path for a block omitted by the observation budget, so it is
	// slightly wider.
	maxInspectionTextBytes = 32 * 1024
)

const (
	treeTextIndentUnit = "                                                                "
	treeTextHexDigits  = "0123456789abcdef"
)

// renderTreeText projects the tree or inspection material already present in
// one result as a deterministic line-oriented block. It never captures, never
// expands a delta and keeps no state: the same result always renders to the
// same bytes. It returns the empty string when the result carries no tree.
func renderTreeText(result *protocol.Result) string {
	if result == nil {
		return ""
	}
	if observation := result.Observation; observation != nil && observation.Tree != nil {
		switch {
		case observation.Tree.Full != nil:
			return renderFullTreeText(observation.ID, observation.Tree.Full)
		case observation.Tree.Delta != nil:
			return renderDeltaTreeText(observation.ID, observation.Tree.Delta)
		}
	}
	if result.Inspection != nil {
		return renderInspectionText(result.Inspection)
	}
	return ""
}

// treeTextBuffer accumulates one block while counting every byte even after it
// stops retaining content. A block that crosses its limit is discarded whole;
// the caller only publishes the counted size in an omitted line.
type treeTextBuffer struct {
	limit   int
	total   int
	store   []byte
	dropped bool
}

func (b *treeTextBuffer) write(value string) {
	b.total += len(value)
	if b.dropped {
		return
	}
	if b.total > b.limit {
		b.dropped = true
		b.store = nil
		return
	}
	b.store = append(b.store, value...)
}

func (b *treeTextBuffer) writeInt(value int) {
	b.write(strconv.Itoa(value))
}

func (b *treeTextBuffer) writeBool(value bool) {
	if value {
		b.write("true")
		return
	}
	b.write("false")
}

// treeTextBlock is one header + blank line + body + trailer. The trailer line
// ends with a newline; a missing trailer in host output means host truncation.
type treeTextBlock struct {
	header  string
	body    treeTextBuffer
	trailer string
	limit   int
}

func (b *treeTextBlock) finish(omitted func(total int) string) string {
	total := len(b.header) + 2 + b.body.total + len(b.trailer) + 1
	if b.body.dropped || total > b.limit {
		return omitted(total)
	}
	var builder strings.Builder
	builder.Grow(total)
	builder.WriteString(b.header)
	builder.WriteString("\n\n")
	builder.WriteString(string(b.body.store))
	builder.WriteString(b.trailer)
	builder.WriteByte('\n')
	return builder.String()
}

func renderFullTreeText(id string, tree *protocol.Tree) string {
	if tree == nil || !validateNodeRefs(tree.Root) {
		return observationRefFormatLine(id, "")
	}
	scope := tree.Scope
	if scope == "" {
		scope = protocol.Scope
	}
	body := treeTextBuffer{limit: maxObservationTreeTextBytes}
	nodes := writeSubtree(&body, tree.Root, "")
	header := "tree v1 format=full id=" + id +
		" scope=" + scope + " nodes=" + strconv.Itoa(nodes)
	block := treeTextBlock{
		header: header, body: body, trailer: "end tree", limit: maxObservationTreeTextBytes,
	}
	return block.finish(func(total int) string {
		return "tree v1 omitted format=full id=" + id +
			" nodes=" + strconv.Itoa(nodes) +
			" bytes=" + strconv.Itoa(total) +
			" limit=" + strconv.Itoa(maxObservationTreeTextBytes) + " use=inspect\n"
	})
}

func renderDeltaTreeText(id string, delta *protocol.Delta) string {
	base := ""
	if delta != nil {
		base = delta.Base
	}
	if delta == nil || !validateDeltaRefs(delta) {
		return observationRefFormatLine(id, base)
	}
	changes := len(delta.Set) + len(delta.Add) + len(delta.Remove) +
		len(delta.Children) + len(delta.Replace)
	body := treeTextBuffer{limit: maxObservationTreeTextBytes}
	writeDeltaBody(&body, delta)
	header := "tree v1 format=delta id=" + id +
		" base=" + delta.Base +
		" scope=" + protocol.Scope + " changes=" + strconv.Itoa(changes)
	block := treeTextBlock{
		header: header, body: body, trailer: "end tree", limit: maxObservationTreeTextBytes,
	}
	return block.finish(func(total int) string {
		return "tree v1 omitted format=delta id=" + id +
			" base=" + base + " changes=" + strconv.Itoa(changes) +
			" bytes=" + strconv.Itoa(total) +
			" limit=" + strconv.Itoa(maxObservationTreeTextBytes) + " use=inspect\n"
	})
}

func renderInspectionText(view *protocol.InspectionView) string {
	if view == nil {
		return ""
	}
	if !validateInspectionRefs(view) {
		return inspectionRefFormatLine(view)
	}
	body := treeTextBuffer{limit: maxInspectionTextBytes}
	nodes := 0
	treeScope := protocol.Scope
	extra := ""
	switch view.Scope {
	case "tree":
		if view.Tree != nil {
			if view.Tree.Scope != "" {
				treeScope = view.Tree.Scope
			}
			nodes = writeSubtree(&body, view.Tree.Root, "")
		}
	case "subtree":
		if view.Root != nil {
			extra = " root=" + view.Root.Ref
			nodes = writeSubtree(&body, view.Root, "")
		}
	case "query":
		matches := len(view.Matches)
		extra = " matches=" + strconv.Itoa(matches)
		if view.HasTotal {
			extra += " total=" + strconv.Itoa(view.Total)
		}
		if view.Truncated != nil {
			extra += " truncated=" + boolText(*view.Truncated)
		}
		for index, match := range view.Matches {
			if index > 0 {
				body.write("\n")
			}
			body.write("match ")
			body.writeInt(index + 1)
			body.write("/")
			body.writeInt(matches)
			body.write(" path=[")
			for pathIndex, ref := range match.Path {
				if pathIndex > 0 {
					body.write(" ")
				}
				body.write(ref)
			}
			body.write("]\n")
			nodes += writeSubtree(&body, match.Node, "")
		}
	}
	header := "inspect v1 scope=" + view.Scope +
		" observation=" + view.ObservationID +
		" revision=" + strconv.Itoa(view.Revision) +
		" captured_at=" + view.CapturedAt +
		" tree_scope=" + treeScope + extra +
		" nodes=" + strconv.Itoa(nodes)
	block := treeTextBlock{
		header: header, body: body, trailer: "end inspect", limit: maxInspectionTextBytes,
	}
	return block.finish(func(total int) string {
		return "inspect v1 omitted scope=" + view.Scope +
			" observation=" + view.ObservationID +
			" revision=" + strconv.Itoa(view.Revision) +
			" nodes=" + strconv.Itoa(nodes) +
			" bytes=" + strconv.Itoa(total) +
			" limit=" + strconv.Itoa(maxInspectionTextBytes) + " use=query\n"
	})
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func observationRefFormatLine(id, base string) string {
	line := "tree v1 omitted reason=ref_format"
	if id != "" {
		line += " id=" + id
	}
	if base != "" {
		line += " base=" + base
	}
	return line + "\n"
}

func inspectionRefFormatLine(view *protocol.InspectionView) string {
	line := "inspect v1 omitted reason=ref_format"
	if view.ObservationID != "" {
		line += " observation=" + view.ObservationID
	}
	if view.Scope != "" {
		line += " scope=" + view.Scope
	}
	return line + "\n"
}

// writeDeltaBody writes the five change blocks in protocol.Delta field order:
// set, add, remove, children, replace.
func writeDeltaBody(body *treeTextBuffer, delta *protocol.Delta) {
	for _, ref := range sortedDeltaKeys(delta.Set) {
		body.write("~ ")
		body.write(ref)
		writeSetNode(body, delta.Set[ref])
		body.write("\n")
	}
	for _, node := range delta.Add {
		writeSubtree(body, node, "+ ")
	}
	for _, ref := range delta.Remove {
		body.write("- ")
		body.write(ref)
		body.write("\n")
	}
	for _, ref := range sortedDeltaKeys(delta.Children) {
		body.write("children ")
		body.write(ref)
		body.write(" = [")
		for index, child := range delta.Children[ref] {
			if index > 0 {
				body.write(" ")
			}
			body.write(child)
		}
		body.write("]\n")
	}
	for _, ref := range sortedDeltaKeys(delta.Replace) {
		writeSubtree(body, delta.Replace[ref], "= ")
	}
}

// writeSetNode writes only fields that are actually set. Each value is the
// complete new value: explicit false/""/[]/null is preserved.
func writeSetNode(body *treeTextBuffer, set protocol.SetNode) {
	if set.Label.Set {
		body.write(" label=")
		if set.Label.Value == nil {
			body.write("null")
		} else {
			writeStringArray(body, set.Label.Value)
		}
	}
	if set.Actions.Set {
		body.write(" actions=")
		if set.Actions.Value == nil {
			body.write("null")
		} else {
			writeActionArray(body, set.Actions.Value)
		}
	}
	if set.Bounds.Set {
		body.write(" bounds=")
		if set.Bounds.Value == nil {
			body.write("null")
		} else {
			writeBounds(body, set.Bounds.Value)
		}
	}
	if set.Enabled.Set {
		body.write(" enabled=")
		if set.Enabled.Value == nil {
			body.write("null")
		} else {
			body.writeBool(*set.Enabled.Value)
		}
	}
	if set.Focused.Set {
		body.write(" focused=")
		if set.Focused.Value == nil {
			body.write("null")
		} else {
			body.writeBool(*set.Focused.Value)
		}
	}
	if set.Selected.Set {
		body.write(" selected=")
		if set.Selected.Value == nil {
			body.write("null")
		} else {
			body.writeBool(*set.Selected.Value)
		}
	}
	if set.Checked.Set {
		body.write(" checked=")
		if set.Checked.Value == nil {
			body.write("null")
		} else {
			body.writeBool(*set.Checked.Value)
		}
	}
	if set.Value.Set {
		body.write(" value=")
		if set.Value.Value == nil {
			body.write("null")
		} else {
			writeJSONString(body, *set.Value.Value)
		}
	}
	if set.Hint.Set {
		body.write(" hint=")
		if set.Hint.Value == nil {
			body.write("null")
		} else {
			writeJSONString(body, *set.Hint.Value)
		}
	}
	if set.Protected.Set {
		body.write(" protected=")
		if set.Protected.Value == nil {
			body.write("null")
		} else {
			body.writeBool(*set.Protected.Value)
		}
	}
	if set.Axis.Set {
		body.write(" axis=")
		if set.Axis.Value == nil {
			body.write("null")
		} else {
			body.write(string(*set.Axis.Value))
		}
	}
	if set.Resource.Set {
		body.write(" resource=")
		if set.Resource.Value == nil {
			body.write("null")
		} else {
			writeJSONString(body, *set.Resource.Value)
		}
	}
}

func writeStringArray(body *treeTextBuffer, values []string) {
	body.write("[")
	for index, value := range values {
		if index > 0 {
			body.write(",")
		}
		writeJSONString(body, value)
	}
	body.write("]")
}

func writeActionArray(body *treeTextBuffer, values []protocol.Action) {
	body.write("[")
	for index, value := range values {
		if index > 0 {
			body.write(",")
		}
		writeJSONString(body, string(value))
	}
	body.write("]")
}

func writeBounds(body *treeTextBuffer, bounds *protocol.Bounds) {
	body.write("[")
	body.writeInt(bounds.Left)
	body.write(",")
	body.writeInt(bounds.Top)
	body.write(",")
	body.writeInt(bounds.Right)
	body.write(",")
	body.writeInt(bounds.Bottom)
	body.write("]")
}

// writeSubtree writes one complete subtree in tree order with an explicit
// stack, so a deep tree cannot overflow the Go stack. The optional prefix is
// used by delta add (+) and replace (=) roots; children never carry it.
func writeSubtree(body *treeTextBuffer, root *protocol.Node, prefix string) int {
	if root == nil {
		return 0
	}
	type pendingNode struct {
		node   *protocol.Node
		depth  int
		prefix string
	}
	stack := []pendingNode{{node: root, prefix: prefix}}
	nodes := 0
	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		writeIndent(body, entry.depth)
		if entry.prefix != "" {
			body.write(entry.prefix)
		}
		writeNodeContent(body, entry.node)
		nodes++
		for index := len(entry.node.Children) - 1; index >= 0; index-- {
			stack = append(stack, pendingNode{
				node:  entry.node.Children[index],
				depth: entry.depth + 1,
			})
		}
	}
	return nodes
}

func writeIndent(body *treeTextBuffer, depth int) {
	spaces := depth * 2
	for spaces > 0 {
		chunk := spaces
		if chunk > len(treeTextIndentUnit) {
			chunk = len(treeTextIndentUnit)
		}
		body.write(treeTextIndentUnit[:chunk])
		spaces -= chunk
	}
}

// writeNodeContent writes the fixed node column order: ref, role, bounds,
// labels, value, hint, actions, then flags. Defaults are omitted exactly as
// protocol.Node.MarshalJSONTo omits them.
func writeNodeContent(body *treeTextBuffer, node *protocol.Node) {
	body.write(node.Ref)
	body.write(" ")
	body.write(string(node.Role))
	body.write(" ")
	if node.Bounds == nil {
		body.write("-")
	} else {
		writeBounds(body, node.Bounds)
	}
	for _, label := range node.Label {
		body.write(" ")
		writeJSONString(body, label)
	}
	if node.Value != nil {
		body.write(" value=")
		writeJSONString(body, *node.Value)
	}
	if node.Hint != nil {
		body.write(" hint=")
		writeJSONString(body, *node.Hint)
	}
	for _, action := range node.Actions {
		body.write(" ")
		body.write(string(action))
	}
	if node.Enabled == nil {
		body.write(" enabled=null")
	} else if !*node.Enabled {
		body.write(" enabled=false")
	}
	writeDefaultFalseFlag(body, "focused", node.Focused)
	writeDefaultFalseFlag(body, "selected", node.Selected)
	writeDefaultFalseFlag(body, "protected", node.Protected)
	if node.Checked != nil {
		body.write(" checked=")
		body.writeBool(*node.Checked)
	}
	if node.Axis != "" {
		body.write(" axis=")
		body.write(string(node.Axis))
	}
	if node.Resource != nil {
		body.write(" resource=")
		writeJSONString(body, *node.Resource)
	}
	if node.Context {
		body.write(" context")
	}
	body.write("\n")
}

// writeDefaultFalseFlag writes a true or null value; a known false is the
// default and is omitted.
func writeDefaultFalseFlag(body *treeTextBuffer, name string, value *bool) {
	if value == nil {
		body.write(" ")
		body.write(name)
		body.write("=null")
		return
	}
	if *value {
		body.write(" ")
		body.write(name)
		body.write("=true")
	}
}

// writeJSONString writes a JSON string literal without encoding/json's HTML
// escaping. Invalid UTF-8 becomes U+FFFD; control characters use the short
// escapes for newline, tab and carriage return and \u00XX otherwise.
func writeJSONString(body *treeTextBuffer, value string) {
	body.write("\"")
	start := 0
	for index := 0; index < len(value); {
		character := value[index]
		if character < utf8.RuneSelf {
			escaped := ""
			switch character {
			case '"':
				escaped = "\\\""
			case '\\':
				escaped = "\\\\"
			case '\n':
				escaped = "\\n"
			case '\t':
				escaped = "\\t"
			case '\r':
				escaped = "\\r"
			default:
				if character < 0x20 {
					escaped = "\\u00" +
						string(treeTextHexDigits[character>>4]) +
						string(treeTextHexDigits[character&0x0f])
				}
			}
			if escaped != "" {
				if start < index {
					body.write(value[start:index])
				}
				body.write(escaped)
				index++
				start = index
				continue
			}
			index++
			continue
		}
		_, size := utf8.DecodeRuneInString(value[index:])
		if size == 1 {
			if start < index {
				body.write(value[start:index])
			}
			body.write("\uFFFD")
			index++
			start = index
			continue
		}
		index += size
	}
	if start < len(value) {
		body.write(value[start:])
	}
	body.write("\"")
}

func validatePublicRef(ref string) bool {
	if len(ref) < 2 || ref[0] != 'n' || ref[1] < '1' || ref[1] > '9' {
		return false
	}
	for index := 1; index < len(ref); index++ {
		if ref[index] < '0' || ref[index] > '9' {
			return false
		}
	}
	return true
}

// validateNodeRefs checks every node in one subtree iteratively.
func validateNodeRefs(root *protocol.Node) bool {
	if root == nil {
		return false
	}
	stack := []*protocol.Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node == nil || !validatePublicRef(node.Ref) {
			return false
		}
		stack = append(stack, node.Children...)
	}
	return true
}

func validateDeltaRefs(delta *protocol.Delta) bool {
	for ref := range delta.Set {
		if !validatePublicRef(ref) {
			return false
		}
	}
	for _, node := range delta.Add {
		if !validateNodeRefs(node) {
			return false
		}
	}
	for _, ref := range delta.Remove {
		if !validatePublicRef(ref) {
			return false
		}
	}
	for ref, children := range delta.Children {
		if !validatePublicRef(ref) {
			return false
		}
		for _, child := range children {
			if !validatePublicRef(child) {
				return false
			}
		}
	}
	for ref, node := range delta.Replace {
		if !validatePublicRef(ref) || !validateNodeRefs(node) {
			return false
		}
	}
	return true
}

func validateInspectionRefs(view *protocol.InspectionView) bool {
	switch view.Scope {
	case "tree":
		if view.Tree != nil && view.Tree.Root != nil && !validateNodeRefs(view.Tree.Root) {
			return false
		}
	case "subtree":
		if view.Root != nil && !validateNodeRefs(view.Root) {
			return false
		}
	case "query":
		for _, match := range view.Matches {
			for _, ref := range match.Path {
				if !validatePublicRef(ref) {
					return false
				}
			}
			if match.Node == nil || !validateNodeRefs(match.Node) {
				return false
			}
		}
	}
	return true
}

func sortedDeltaKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		return lessPublicRef(keys[left], keys[right])
	})
	return keys
}

// lessPublicRef orders validated n-prefixed refs numerically. Because leading
// zeros are rejected, length then lexicographic order is numeric order and
// cannot overflow an integer parser.
func lessPublicRef(left, right string) bool {
	if len(left) != len(right) {
		return len(left) < len(right)
	}
	return left < right
}
