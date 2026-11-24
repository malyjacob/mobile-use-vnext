package mcp

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
	"github.com/malyjacob/mobile-use-vnext/internal/treetext"
)

const (
	treeTextIndentUnit = "                                                                "
	treeTextHexDigits  = "0123456789abcdef"
)

// renderTreeText projects the tree or inspection material already present in
// one result as a deterministic line-oriented block. It never captures, never
// expands a delta and keeps no state: the same result always renders to the same
// bytes. A false result means the caller must use canonical JSON.
func renderTreeText(result *protocol.Result) (string, bool) {
	if result == nil {
		return "", false
	}
	if observation := result.Observation; observation != nil && observation.Tree != nil {
		switch {
		case observation.Tree.Full != nil && observation.Tree.Delta == nil:
			return treetext.EncodeFull(observation.ID, observation.Tree.Full)
		case observation.Tree.Delta != nil && observation.Tree.Full == nil:
			return treetext.EncodeDelta(observation.ID, observation.Tree.Delta)
		default:
			return "", false
		}
	}
	if result.Inspection != nil {
		return renderInspectionText(result.Inspection)
	}
	return "", false
}

// treeTextBuffer accumulates one complete block. The total is retained for one
// allocation in finish; content is never discarded based on size.
type treeTextBuffer struct {
	total int
	store []byte
}

func (b *treeTextBuffer) write(value string) {
	b.total += len(value)
	b.store = append(b.store, value...)
}

func (b *treeTextBuffer) writeInt(value int) {
	b.write(strconv.Itoa(value))
}

// treeTextBlock is one header, a blank line, the body and a trailer. The trailer
// line ends with a newline; a missing trailer in host output means host
// truncation.
type treeTextBlock struct {
	header  string
	body    treeTextBuffer
	trailer string
}

func (b *treeTextBlock) finish() (string, bool) {
	header := b.header + "\n\n"
	total := len(header) + b.body.total + len(b.trailer) + 1
	var builder strings.Builder
	builder.Grow(total)
	builder.WriteString(header)
	builder.Write(b.body.store)
	builder.WriteString(b.trailer)
	builder.WriteByte('\n')
	return builder.String(), true
}

func renderInspectionText(view *protocol.InspectionView) (string, bool) {
	if !validateInspection(view) {
		return "", false
	}
	body := treeTextBuffer{}
	nodes, entries := 0, 0
	treeScope := protocol.Scope
	viewName := view.View
	extra := ""
	switch view.Scope {
	case "tree":
		treeScope = view.Tree.Scope
		viewName = view.Tree.View
		nodes, entries = writeSubtree(&body, view.Tree.Root, "")
	case "subtree":
		extra = " root=" + view.Root.Ref
		nodes, entries = writeSubtree(&body, view.Root, "")
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
			matchNodes, matchEntries := writeSubtree(&body, match.Node, "")
			nodes += matchNodes
			entries += matchEntries
		}
	}
	header := "inspect v3 scope=" + view.Scope +
		" observation=" + view.ObservationID +
		" revision=" + strconv.Itoa(view.Revision) +
		" captured_at=" + view.CapturedAt +
		" tree_scope=" + treeScope +
		" view=" + viewName + extra +
		" nodes=" + strconv.Itoa(nodes) + " entries=" + strconv.Itoa(entries)
	block := treeTextBlock{header: header, body: body, trailer: "end inspect"}
	return block.finish()
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
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

// writeSubtree writes one explicit inspect v3 subtree.
func writeSubtree(body *treeTextBuffer, root *protocol.ReadNode, prefix string) (int, int) {
	if root == nil {
		return 0, 0
	}
	type pendingNode struct {
		node   *protocol.ReadNode
		depth  int
		prefix string
	}
	stack := []pendingNode{{node: root, prefix: prefix}}
	nodes, entries := 0, 0
	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		writeIndent(body, entry.depth)
		if entry.prefix != "" {
			body.write(entry.prefix)
		}
		writeNodeContent(body, entry.node)
		nodes += len(entry.node.Refs())
		entries++

		for index := len(entry.node.Children) - 1; index >= 0; index-- {
			stack = append(stack, pendingNode{
				node:  entry.node.Children[index],
				depth: entry.depth + 1,
			})
		}
	}
	return nodes, entries
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

func writeNodeContent(body *treeTextBuffer, node *protocol.ReadNode) {
	if node.IsChain() {
		if isAnonymousDefaultViewChain(node) {
			writeChainRefs(body, node.Chain)
			body.write("\n")
			return
		}
		body.write("chain ")
		writeChainRefs(body, node.Chain)
		body.write(" ")
		body.write(string(node.Role))
		writeNodeColumns(body, node)
		body.write("\n")
		return
	}
	body.write(node.Ref)
	if !omitsNodeRole(node) {
		body.write(" ")
		body.write(string(node.Role))
	}
	writeNodeColumns(body, node)
	body.write("\n")
}

func writeChainRefs(body *treeTextBuffer, refs []string) {
	body.write("[")
	for index, ref := range refs {
		if index > 0 {
			body.write(" ")
		}
		body.write(ref)
	}
	body.write("]")
}

// isAnonymousDefaultViewChain recognizes the one chain definition whose role
// and hidden shared columns add no information: an anonymous default view with
// every other shared field at its complete-node default.
func isAnonymousDefaultViewChain(node *protocol.ReadNode) bool {
	return len(node.Chain) > 0 &&
		node.Role == protocol.RoleView &&
		node.Bounds.Kind == protocol.GeometryHidden &&
		len(node.Label) == 0 &&
		node.Value == nil &&
		node.Hint == nil &&
		len(node.Actions) == 0 &&
		node.Enabled != nil && *node.Enabled &&
		node.Focused != nil && !*node.Focused &&
		node.Selected != nil && !*node.Selected &&
		node.Protected != nil && !*node.Protected &&
		node.Checked != nil && !*node.Checked &&
		node.Axis == "" &&
		node.Resource == nil &&
		!node.Context
}

// omitsNodeRole applies the v3 recovery rule to an ordinary node. The first
// surviving column determines the default role. An unquoted role word in that
// position is always the explicit role, so it can never be omitted, while a
// quoted label defaults to text and every other first column defaults to view.
func omitsNodeRole(node *protocol.ReadNode) bool {
	if nodeFirstColumnIsRole(node) {
		return false
	}
	defaultRole := protocol.RoleView
	if node.Bounds.Kind == protocol.GeometryHidden && len(node.Label) > 0 {
		defaultRole = protocol.RoleText
	}
	return node.Role == defaultRole
}

func nodeFirstColumnIsRole(node *protocol.ReadNode) bool {
	if node.Bounds.Kind != protocol.GeometryHidden || len(node.Label) > 0 ||
		node.Value != nil || node.Hint != nil || len(node.Actions) == 0 {
		return false
	}
	return protocol.Role(node.Actions[0]).Valid()
}

func writeNodeColumns(body *treeTextBuffer, node *protocol.ReadNode) {
	writeNodeGeometry(body, node)
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
	writeExplicitActions(body, node.Actions)
	if node.Enabled == nil {
		body.write(" enabled=null")
	} else if !*node.Enabled {
		body.write(" enabled=false")
	}
	writeDefaultFalseFlag(body, "focused", node.Focused)
	writeDefaultFalseFlag(body, "selected", node.Selected)
	writeDefaultFalseFlag(body, "protected", node.Protected)
	// checked defaults to known false for a complete node; only true and
	// unknown are written.
	if node.Checked == nil {
		body.write(" checked=null")
	} else if *node.Checked {
		body.write(" checked=true")
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
}

func writeNodeGeometry(body *treeTextBuffer, node *protocol.ReadNode) {
	if node.Bounds.Kind == protocol.GeometryHidden {
		return
	}
	body.write(" ")
	if node.Bounds.Kind == protocol.GeometryUnknown {
		body.write("-")
		return
	}
	writeBounds(body, &node.Bounds.Rect)
}

func writeExplicitActions(body *treeTextBuffer, actions []protocol.Action) {
	for _, action := range actions {
		body.write(" ")
		body.write(string(action))
	}
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
	return protocol.ValidPublicRef(ref)
}

func validateTreeTextToken(value string) bool {
	return resultToken(value)
}

// validateV4ObservationToken is shared by result grouping and tree rendering.
func validateV4ObservationToken(value string) bool {
	return len(value) > 1 && len(value) <= protocol.MaxReferenceLen &&
		value[0] == 'O' && validateTreeTextToken(value)
}

func validateBounds(bounds *protocol.Bounds) bool {
	return bounds == nil ||
		(bounds.Right >= bounds.Left && bounds.Bottom >= bounds.Top)
}

// validateReadNodeTree checks one definition subtree iteratively. Full trees
// rely on ReadTree.Validate for global ref uniqueness, and inspection keeps its
// existing subtree semantics.
func validateReadNodeTree(root *protocol.ReadNode, requireRoot bool) bool {
	if root == nil {
		return false
	}
	type pendingNode struct {
		node       *protocol.ReadNode
		mustBeRoot bool
	}
	stack := []pendingNode{{node: root, mustBeRoot: requireRoot}}
	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := entry.node
		if node == nil || !node.Role.Valid() {
			return false
		}
		if len(node.Chain) > 0 {
			if node.Ref != "" || len(node.Chain) < 2 || entry.mustBeRoot {
				return false
			}
			for _, ref := range node.Chain {
				if !validatePublicRef(ref) {
					return false
				}
			}
		} else if !validatePublicRef(node.Ref) {
			return false
		}
		if (entry.mustBeRoot && (node.IsChain() || node.Role != protocol.RoleRoot)) ||
			!validateBoundsForState(node.Bounds) || (node.Axis != "" && !node.Axis.Valid()) {
			return false
		}
		for _, action := range node.Actions {
			if !action.Valid() {
				return false
			}
		}
		for _, child := range node.Children {
			stack = append(stack, pendingNode{node: child})
		}
	}
	return true
}

func validateBoundsForState(bounds protocol.ReadBounds) bool {
	switch bounds.Kind {
	case protocol.GeometryHidden, protocol.GeometryUnknown, protocol.GeometryPoint:
		return true
	case protocol.GeometryKnown:
		return validateBounds(&bounds.Rect)
	}
	return false
}

func validateFullTree(tree *protocol.ReadTree) bool {
	return tree != nil && tree.Format == "full" && tree.Scope == protocol.Scope &&
		(tree.View == protocol.ViewCompact || tree.View == protocol.ViewDetailed) &&
		validateReadNodeTree(tree.Root, true) && tree.Validate() == nil
}

func validateInspection(view *protocol.InspectionView) bool {
	if view == nil || !validateTreeTextToken(view.ObservationID) ||
		!validateTreeTextToken(view.CapturedAt) {
		return false
	}
	switch view.Scope {
	case "tree":
		if view.View != "" {
			return false
		}
		return validateFullTree(view.Tree) && view.Tree.View == protocol.ViewDetailed
	case "subtree":
		if view.View != protocol.ViewDetailed {
			return false
		}
		return validateReadNodeTree(view.Root, false) && detailedRectanglesOnly(view.Root)
	case "query":
		if view.View != protocol.ViewDetailed {
			return false
		}
		for _, match := range view.Matches {
			for _, ref := range match.Path {
				if !validatePublicRef(ref) {
					return false
				}
			}
			if !validateReadNodeTree(match.Node, false) || !detailedRectanglesOnly(match.Node) {
				return false
			}
		}
		return true
	}
	return false
}

// detailedRectanglesOnly reports whether a detailed subtree delivers every known
// rectangle. The detailed inspection view keeps the complete node set, so hidden
// and point geometry are both invalid there.
func detailedRectanglesOnly(root *protocol.ReadNode) bool {
	type pendingNode struct{ node *protocol.ReadNode }
	stack := []pendingNode{{node: root}}
	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if entry.node == nil || entry.node.IsChain() {
			return false
		}
		switch entry.node.Bounds.Kind {
		case protocol.GeometryKnown, protocol.GeometryUnknown:
		default:
			return false
		}
		for _, child := range entry.node.Children {
			stack = append(stack, pendingNode{node: child})
		}
	}
	return true
}
