package mcp

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
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
			return renderFullTreeText(observation.ID, observation.Tree.Full)
		case observation.Tree.Delta != nil && observation.Tree.Full == nil:
			return renderDeltaTreeText(observation.ID, observation.Tree.Delta)
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

func (b *treeTextBuffer) writeBool(value bool) {
	if value {
		b.write("true")
		return
	}
	b.write("false")
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

func renderFullTreeText(id string, tree *protocol.ReadTree) (string, bool) {
	if !validateV4ObservationToken(id) || !validateFullTree(tree) || tree.View != protocol.ViewCompact {
		return "", false
	}
	body := treeTextBuffer{}
	nodes, _ := writeV4Subtree(&body, tree.Root, "")
	header := "tree v4 full nodes=" + strconv.Itoa(nodes)
	block := treeTextBlock{header: header, body: body, trailer: "end tree"}
	return block.finish()
}

func renderDeltaTreeText(id string, delta *protocol.ReadDelta) (string, bool) {
	if !validateV4ObservationToken(id) || !validateDelta(delta) {
		return "", false
	}
	changes := len(delta.Set) + len(delta.Add) + len(delta.Remove) +
		len(delta.Children) + len(delta.Replace)
	body := treeTextBuffer{}
	writeDeltaBody(&body, delta)
	header := "tree v4 delta base=" + v4ShortDeltaBase(id, delta.Base) +
		" changes=" + strconv.Itoa(changes)
	block := treeTextBlock{header: header, body: body, trailer: "end tree"}
	return block.finish()
}

// v4ShortDeltaBase uses the current full O to abbreviate a historical base only
// when both end in a canonical positive uint64 sequence and their O<instance>
// prefixes are byte-identical. Any ambiguity keeps the real full base.
func v4ShortDeltaBase(current, base string) string {
	currentPrefix, _, currentOK := v4SplitObservationSequence(current)
	basePrefix, baseSequence, baseOK := v4SplitObservationSequence(base)
	if !currentOK || !baseOK || currentPrefix != basePrefix {
		return base
	}
	return "_" + baseSequence
}

func v4SplitObservationSequence(value string) (string, string, bool) {
	if !validateV4ObservationToken(value) {
		return "", "", false
	}
	index := strings.LastIndexByte(value, '_')
	if index <= 1 || index == len(value)-1 {
		return "", "", false
	}
	sequence := value[index+1:]
	if sequence[0] == '0' {
		return "", "", false
	}
	parsed, err := strconv.ParseUint(sequence, 10, 64)
	if err != nil || parsed == 0 {
		return "", "", false
	}
	return value[:index], sequence, true
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

// writeDeltaBody writes the five change blocks in protocol.ReadDelta field
// order: set, add, remove, children, replace.
func writeDeltaBody(body *treeTextBuffer, delta *protocol.ReadDelta) {
	for _, ref := range sortedDeltaKeys(delta.Set) {
		body.write("~ ")
		body.write(ref)
		writeSetNode(body, delta.Set[ref])
		body.write("\n")
	}
	for _, node := range delta.Add {
		writeV4Subtree(body, node, "+ ")
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
		writeV4Subtree(body, delta.Replace[ref], "= ")
	}
}

// writeSetNode writes only fields that are actually set. Each value is the
// complete new value: explicit false/""/[]/null is preserved, and bounds uses
// "hidden" for the policy-hidden geometry state.
func writeSetNode(body *treeTextBuffer, set protocol.ReadSetNode) {
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
		switch set.Bounds.Value.Kind {
		case protocol.GeometryHidden:
			body.write("hidden")
		case protocol.GeometryUnknown:
			body.write("null")
		default:
			writeBounds(body, &set.Bounds.Value.Rect)
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

type treeTextStyle uint8

const (
	treeTextStyleInspectV3 treeTextStyle = iota
	treeTextStyleTreeV4
)

// writeSubtree writes one explicit inspect v3 subtree. writeV4Subtree selects
// the compact tree style, while both share the same iterative traversal and
// node field writer.
func writeSubtree(body *treeTextBuffer, root *protocol.ReadNode, prefix string) (int, int) {
	return writeSubtreeStyle(body, root, prefix, treeTextStyleInspectV3)
}

// Each v4 definition owns an ancestor stack, so delta + and = roots never
// inherit @ references from another definition or historical result. Hidden
// and unknown geometry do not clear the nearest known ancestor.
func writeV4Subtree(body *treeTextBuffer, root *protocol.ReadNode, prefix string) (int, int) {
	return writeSubtreeStyle(body, root, prefix, treeTextStyleTreeV4)
}

func writeSubtreeStyle(
	body *treeTextBuffer,
	root *protocol.ReadNode,
	prefix string,
	style treeTextStyle,
) (int, int) {
	if root == nil {
		return 0, 0
	}
	type pendingNode struct {
		node           *protocol.ReadNode
		depth          int
		prefix         string
		ancestorKnown  bool
		ancestorBounds protocol.Bounds
	}
	stack := []pendingNode{{node: root, prefix: prefix}}
	nodes, entries := 0, 0
	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		referenceBounds := style == treeTextStyleTreeV4 &&
			entry.node.Bounds.Kind == protocol.GeometryKnown &&
			!entry.node.IsChain() && entry.ancestorKnown &&
			entry.node.Bounds.Rect == entry.ancestorBounds
		writeTreeTextIndent(body, entry.depth, style)
		if entry.prefix != "" {
			body.write(entry.prefix)
		}
		writeNodeContentStyle(body, entry.node, referenceBounds, style)
		nodes += len(entry.node.Refs())
		entries++

		childKnown := entry.ancestorKnown
		childBounds := entry.ancestorBounds
		if entry.node.Bounds.Kind == protocol.GeometryKnown {
			childKnown = true
			childBounds = entry.node.Bounds.Rect
		}
		for index := len(entry.node.Children) - 1; index >= 0; index-- {
			stack = append(stack, pendingNode{
				node:           entry.node.Children[index],
				depth:          entry.depth + 1,
				ancestorKnown:  childKnown,
				ancestorBounds: childBounds,
			})
		}
	}
	return nodes, entries
}

func writeIndent(body *treeTextBuffer, depth int) {
	writeTreeTextIndent(body, depth, treeTextStyleInspectV3)
}

func writeTreeTextIndent(body *treeTextBuffer, depth int, style treeTextStyle) {
	spaces := depth
	if style == treeTextStyleInspectV3 {
		spaces *= 2
	}
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
	writeNodeContentStyle(body, node, false, treeTextStyleInspectV3)
}

func writeNodeContentStyle(
	body *treeTextBuffer,
	node *protocol.ReadNode,
	referenceBounds bool,
	style treeTextStyle,
) {
	if node.IsChain() {
		if style == treeTextStyleInspectV3 && isAnonymousDefaultViewChain(node) {
			writeChainRefs(body, node.Chain)
			body.write("\n")
			return
		}
		body.write("chain ")
		writeChainRefs(body, node.Chain)
		body.write(" ")
		body.write(string(node.Role))
		writeNodeColumnsStyle(body, node, false, style, true)
		body.write("\n")
		return
	}
	body.write(node.Ref)
	if !omitsNodeRole(node) {
		body.write(" ")
		body.write(string(node.Role))
	}
	writeNodeColumnsStyle(body, node, referenceBounds, style, false)
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

// writeNodeColumns writes the inspect v3 columns. writeNodeColumnsStyle keeps
// the complete field sequence single-sourced and dispatches only geometry and
// default-action rendering by style.
func writeNodeColumns(body *treeTextBuffer, node *protocol.ReadNode) {
	writeNodeColumnsStyle(body, node, false, treeTextStyleInspectV3, true)
}

func writeNodeColumnsStyle(
	body *treeTextBuffer,
	node *protocol.ReadNode,
	referenceBounds bool,
	style treeTextStyle,
	explicitActions bool,
) {
	writeNodeGeometry(body, node, referenceBounds, style)
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
	if style == treeTextStyleTreeV4 && !explicitActions {
		writeV4OrdinaryActions(body, node.Role, node.Actions)
	} else {
		writeExplicitActions(body, node.Actions)
	}
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

func writeNodeGeometry(
	body *treeTextBuffer,
	node *protocol.ReadNode,
	referenceBounds bool,
	style treeTextStyle,
) {
	if node.Bounds.Kind == protocol.GeometryHidden {
		return
	}
	body.write(" ")
	if node.Bounds.Kind == protocol.GeometryUnknown {
		body.write("-")
		return
	}
	if style == treeTextStyleTreeV4 {
		if referenceBounds {
			body.write("@")
		} else {
			writeV4Rectangle(body, &node.Bounds.Rect)
		}
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

// writeV4OrdinaryActions makes one decision pass over the source list. It
// records whether the default occurs exactly once, whether the other actions
// are strictly ascending in source order, and where a restored default would
// be inserted. Duplicate or reordered actions stay fully explicit.
func writeV4OrdinaryActions(
	body *treeTextBuffer,
	role protocol.Role,
	actions []protocol.Action,
) {
	defaultAction, hasDefault := v4DefaultRoleAction(role)
	if !hasDefault {
		writeExplicitActions(body, actions)
		return
	}
	defaultRank := treeV4ActionRank(defaultAction)
	defaults := 0
	defaultPosition := -1
	insertAt := 0
	seenNonDefault := 0
	lastRank := -1
	sourceOrder := true
	for _, action := range actions {
		if action == defaultAction {
			defaults++
			if defaults == 1 {
				defaultPosition = seenNonDefault
			}
			continue
		}
		rank := treeV4ActionRank(action)
		if rank < 0 || rank <= lastRank {
			sourceOrder = false
		}
		lastRank = rank
		if rank < defaultRank {
			insertAt++
		}
		seenNonDefault++
	}
	switch {
	case defaults == 0:
		writeExplicitActions(body, actions)
		body.write(" !")
		body.write(string(defaultAction))
	case defaults == 1 && sourceOrder && defaultPosition == insertAt:
		for _, action := range actions {
			if action != defaultAction {
				body.write(" ")
				body.write(string(action))
			}
		}
	default:
		writeExplicitActions(body, actions)
	}
}

func v4DefaultRoleAction(role protocol.Role) (protocol.Action, bool) {
	switch role {
	case protocol.RoleButton:
		return protocol.ActionTap, true
	case protocol.RoleInput:
		return protocol.ActionText, true
	case protocol.RoleScroll:
		return protocol.ActionSwipe, true
	default:
		return "", false
	}
}

func treeV4ActionRank(action protocol.Action) int {
	switch action {
	case protocol.ActionTap:
		return 0
	case protocol.ActionLongPress:
		return 1
	case protocol.ActionText:
		return 2
	case protocol.ActionSwipe:
		return 3
	default:
		return -1
	}
}

func writeV4Rectangle(body *treeTextBuffer, bounds *protocol.Bounds) {
	body.writeInt(bounds.Left)
	body.write(",")
	body.writeInt(bounds.Top)
	body.write(",")
	body.writeInt(bounds.Right)
	body.write(",")
	body.writeInt(bounds.Bottom)
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
	return validateReadNodeTreeDetails(root, requireRoot, false)
}

// v4 delta definitions are independent serialized subtrees, so duplicate refs
// within one add or replace definition cannot be represented as a legal block.
func validateV4DeltaNodeTree(root *protocol.ReadNode) bool {
	return validateReadNodeTreeDetails(root, false, true)
}

func validateReadNodeTreeDetails(
	root *protocol.ReadNode,
	requireRoot bool,
	checkUniqueRefs bool,
) bool {
	if root == nil {
		return false
	}
	type pendingNode struct {
		node       *protocol.ReadNode
		mustBeRoot bool
	}
	stack := []pendingNode{{node: root, mustBeRoot: requireRoot}}
	var seenRefs map[string]bool
	if checkUniqueRefs {
		seenRefs = make(map[string]bool)
	}
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
		if seenRefs != nil {
			for _, ref := range node.Refs() {
				if seenRefs[ref] {
					return false
				}
				seenRefs[ref] = true
			}
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
	case protocol.GeometryHidden, protocol.GeometryUnknown:
		return true
	case protocol.GeometryKnown:
		return validateBounds(&bounds.Rect)
	}
	return false
}

func validateSetNode(set protocol.ReadSetNode) bool {
	if set.Actions.Set && set.Actions.Value != nil {
		for _, action := range set.Actions.Value {
			if !action.Valid() {
				return false
			}
		}
	}
	if set.Bounds.Set && !validateBoundsForState(set.Bounds.Value) {
		return false
	}
	if set.Axis.Set && set.Axis.Value != nil && !set.Axis.Value.Valid() {
		return false
	}
	return true
}

func validateFullTree(tree *protocol.ReadTree) bool {
	return tree != nil && tree.Format == "full" && tree.Scope == protocol.Scope &&
		(tree.View == protocol.ViewCompact || tree.View == protocol.ViewDetailed) &&
		validateReadNodeTree(tree.Root, true) && tree.Validate() == nil
}

func validateDelta(delta *protocol.ReadDelta) bool {
	if delta == nil || delta.Format != "delta" || !validateV4ObservationToken(delta.Base) ||
		delta.View != protocol.ViewCompact || delta.Validate() != nil {
		return false
	}
	for ref := range delta.Set {
		if !validatePublicRef(ref) || !validateSetNode(delta.Set[ref]) {
			return false
		}
	}
	added := make(map[string]bool, len(delta.Add))
	for _, node := range delta.Add {
		if !validateV4DeltaNodeTree(node) || added[node.Ref] {
			return false
		}
		added[node.Ref] = true
	}
	removed := make(map[string]bool, len(delta.Remove))
	for _, ref := range delta.Remove {
		if removed[ref] {
			return false
		}
		removed[ref] = true
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
		if !validatePublicRef(ref) || !validateV4DeltaNodeTree(node) || node.Ref != ref {
			return false
		}
	}
	return true
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
		return validateReadNodeTree(view.Root, false) && noHiddenGeometry(view.Root)
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
			if !validateReadNodeTree(match.Node, false) || !noHiddenGeometry(match.Node) {
				return false
			}
		}
		return true
	}
	return false
}

// noHiddenGeometry reports whether a detailed subtree delivers every known
// rectangle.
func noHiddenGeometry(root *protocol.ReadNode) bool {
	type pendingNode struct{ node *protocol.ReadNode }
	stack := []pendingNode{{node: root}}
	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if entry.node == nil || entry.node.Bounds.Kind == protocol.GeometryHidden ||
			entry.node.IsChain() {
			return false
		}
		for _, child := range entry.node.Children {
			stack = append(stack, pendingNode{node: child})
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
