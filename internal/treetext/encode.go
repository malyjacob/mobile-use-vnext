// Package treetext encodes ordinary compact trees and deltas as tree v5 text.
package treetext

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	indentUnit = "                                                                "
	hexDigits  = "0123456789abcdef"
)

// EncodeFull renders one complete compact tree as a tree v5 block. The block
// starts with "tree v5 full" and ends with the "end tree" line and newline.
// A false result means the caller must use canonical JSON.
func EncodeFull(observationID string, tree *protocol.ReadTree) (string, bool) {
	if !validObservationID(observationID) || !validFullTree(tree) ||
		tree.View != protocol.ViewCompact {
		return "", false
	}
	body := textBuffer{}
	nodes, _ := writeSubtree(&body, tree.Root, "")
	block := textBlock{
		header:  "tree v5 full nodes=" + strconv.Itoa(nodes),
		body:    body,
		trailer: "end tree",
	}
	return block.finish()
}

// EncodeDelta renders one compact delta as a tree v5 block. observationID is
// the current full O used only to abbreviate a matching delta base.
func EncodeDelta(observationID string, delta *protocol.ReadDelta) (string, bool) {
	if !validObservationID(observationID) || !validDelta(delta) {
		return "", false
	}
	changes := len(delta.Set) + len(delta.Add) + len(delta.Remove) +
		len(delta.Children) + len(delta.Replace)
	body := textBuffer{}
	writeDeltaBody(&body, delta)
	block := textBlock{
		header: "tree v5 delta base=" + shortDeltaBase(observationID, delta.Base) +
			" changes=" + strconv.Itoa(changes),
		body:    body,
		trailer: "end tree",
	}
	return block.finish()
}

type textBuffer struct {
	total int
	store []byte
}

func (b *textBuffer) write(value string) {
	b.total += len(value)
	b.store = append(b.store, value...)
}

func (b *textBuffer) writeInt(value int) {
	b.write(strconv.Itoa(value))
}

func (b *textBuffer) writeBool(value bool) {
	if value {
		b.write("true")
		return
	}
	b.write("false")
}

type textBlock struct {
	header  string
	body    textBuffer
	trailer string
}

func (b *textBlock) finish() (string, bool) {
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

// shortDeltaBase abbreviates a historical base only when both observations
// have a canonical positive sequence suffix and byte-identical O prefixes.
func shortDeltaBase(current, base string) string {
	currentPrefix, _, currentOK := splitObservationSequence(current)
	basePrefix, baseSequence, baseOK := splitObservationSequence(base)
	if !currentOK || !baseOK || currentPrefix != basePrefix {
		return base
	}
	return "_" + baseSequence
}

func splitObservationSequence(value string) (string, string, bool) {
	if !validObservationID(value) {
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

// writeDeltaBody writes change blocks in protocol.ReadDelta field order.
func writeDeltaBody(body *textBuffer, delta *protocol.ReadDelta) {
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

// writeSetNode preserves explicit false, zero, empty, and null assignments.
func writeSetNode(body *textBuffer, set protocol.ReadSetNode) {
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
		case protocol.GeometryPoint:
			writePointArray(body, set.Bounds.Value.Point)
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

func writeStringArray(body *textBuffer, values []string) {
	body.write("[")
	for index, value := range values {
		if index > 0 {
			body.write(",")
		}
		writeJSONString(body, value)
	}
	body.write("]")
}

func writeActionArray(body *textBuffer, values []protocol.Action) {
	body.write("[")
	for index, value := range values {
		if index > 0 {
			body.write(",")
		}
		writeJSONString(body, string(value))
	}
	body.write("]")
}

func writeBounds(body *textBuffer, bounds *protocol.Bounds) {
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

// Each definition owns an ancestor stack, so delta + and = roots never inherit
// @ references from another definition or historical result. The nearest known
// ancestor geometry is compared by kind and payload, so a point only abbreviates
// an equal point and a rectangle only an equal rectangle.
func writeSubtree(body *textBuffer, root *protocol.ReadNode, prefix string) (int, int) {
	if root == nil {
		return 0, 0
	}
	type pendingNode struct {
		node           *protocol.ReadNode
		depth          int
		prefix         string
		ancestorKnown  bool
		ancestorBounds protocol.ReadBounds
	}
	stack := []pendingNode{{node: root, prefix: prefix}}
	nodes, entries := 0, 0
	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		referenceBounds := deliveredGeometryKnown(entry.node.Bounds.Kind) &&
			!entry.node.IsChain() && entry.ancestorKnown &&
			entry.node.Bounds.Equal(entry.ancestorBounds)
		writeIndent(body, entry.depth)
		if entry.prefix != "" {
			body.write(entry.prefix)
		}
		writeNodeContent(body, entry.node, referenceBounds)
		nodes += len(entry.node.Refs())
		entries++

		childKnown := entry.ancestorKnown
		childBounds := entry.ancestorBounds
		if deliveredGeometryKnown(entry.node.Bounds.Kind) {
			childKnown = true
			childBounds = entry.node.Bounds
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

// deliveredGeometryKnown reports whether the kind carries delivered geometry.
func deliveredGeometryKnown(kind protocol.GeometryKind) bool {
	return kind == protocol.GeometryKnown || kind == protocol.GeometryPoint
}

func writeIndent(body *textBuffer, depth int) {
	for depth > 0 {
		chunk := depth
		if chunk > len(indentUnit) {
			chunk = len(indentUnit)
		}
		body.write(indentUnit[:chunk])
		depth -= chunk
	}
}

func writeNodeContent(body *textBuffer, node *protocol.ReadNode, referenceBounds bool) {
	if node.IsChain() {
		body.write("chain ")
		writeChainRefs(body, node.Chain)
		body.write(" ")
		body.write(string(node.Role))
		writeNodeColumns(body, node, false, true)
		body.write("\n")
		return
	}
	body.write(node.Ref)
	if !omitsNodeRole(node) {
		body.write(" ")
		body.write(string(node.Role))
	}
	writeNodeColumns(body, node, referenceBounds, false)
	body.write("\n")
}

func writeChainRefs(body *textBuffer, refs []string) {
	body.write("[")
	for index, ref := range refs {
		if index > 0 {
			body.write(" ")
		}
		body.write(ref)
	}
	body.write("]")
}

// omitsNodeRole applies the v5 recovery rule to an ordinary node. An unquoted
// role word in the first surviving column is always explicit, while a quoted
// label defaults to text and every other first column defaults to view.
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

func writeNodeColumns(
	body *textBuffer,
	node *protocol.ReadNode,
	referenceBounds bool,
	explicitActions bool,
) {
	writeNodeGeometry(body, node, referenceBounds)
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
	if explicitActions {
		writeExplicitActions(body, node.Actions)
	} else {
		writeOrdinaryActions(body, node.Role, node.Actions)
	}
	if node.Enabled == nil {
		body.write(" enabled=null")
	} else if !*node.Enabled {
		body.write(" enabled=false")
	}
	writeDefaultFalseFlag(body, "focused", node.Focused)
	writeDefaultFalseFlag(body, "selected", node.Selected)
	writeDefaultFalseFlag(body, "protected", node.Protected)
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

func writeNodeGeometry(body *textBuffer, node *protocol.ReadNode, referenceBounds bool) {
	if node.Bounds.Kind == protocol.GeometryHidden {
		return
	}
	body.write(" ")
	if node.Bounds.Kind == protocol.GeometryUnknown {
		body.write("-")
		return
	}
	if referenceBounds {
		body.write("@")
		return
	}
	if node.Bounds.Kind == protocol.GeometryPoint {
		writePoint(body, node.Bounds.Point)
		return
	}
	writeRectangle(body, &node.Bounds.Rect)
}

func writeExplicitActions(body *textBuffer, actions []protocol.Action) {
	for _, action := range actions {
		body.write(" ")
		body.write(string(action))
	}
}

// writeOrdinaryActions makes one pass over the source list. It records whether
// the default occurs exactly once, whether other actions are strictly
// ascending, and where a restored default belongs.
func writeOrdinaryActions(body *textBuffer, role protocol.Role, actions []protocol.Action) {
	defaultAction, hasDefault := defaultRoleAction(role)
	if !hasDefault {
		writeExplicitActions(body, actions)
		return
	}
	defaultRank := actionRank(defaultAction)
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
		rank := actionRank(action)
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

func defaultRoleAction(role protocol.Role) (protocol.Action, bool) {
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

func actionRank(action protocol.Action) int {
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

func writeRectangle(body *textBuffer, bounds *protocol.Bounds) {
	body.writeInt(bounds.Left)
	body.write(",")
	body.writeInt(bounds.Top)
	body.write(",")
	body.writeInt(bounds.Right)
	body.write(",")
	body.writeInt(bounds.Bottom)
}

func writePoint(body *textBuffer, point protocol.Point) {
	body.writeInt(point.X)
	body.write(",")
	body.writeInt(point.Y)
}

func writePointArray(body *textBuffer, point protocol.Point) {
	body.write("[")
	writePoint(body, point)
	body.write("]")
}

func writeDefaultFalseFlag(body *textBuffer, name string, value *bool) {
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
// escaping. Invalid UTF-8 becomes U+FFFD.
func writeJSONString(body *textBuffer, value string) {
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
						string(hexDigits[character>>4]) +
						string(hexDigits[character&0x0f])
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

func validObservationID(value string) bool {
	return len(value) > 1 && len(value) <= protocol.MaxReferenceLen &&
		value[0] == 'O' && resultToken(value)
}

func resultToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character <= ' ' || character >= 127 || character == '=' ||
			character == '"' || character == '\\' {
			return false
		}
	}
	return true
}

func validBounds(bounds *protocol.Bounds) bool {
	return bounds == nil ||
		(bounds.Right >= bounds.Left && bounds.Bottom >= bounds.Top)
}

func validReadNodeTree(root *protocol.ReadNode, requireRoot bool) bool {
	return validReadNodeTreeDetails(root, requireRoot, false)
}

// Delta add and replace definitions are independent serialized subtrees.
func validDeltaNodeTree(root *protocol.ReadNode) bool {
	return validReadNodeTreeDetails(root, false, true)
}

func validReadNodeTreeDetails(
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
				if !protocol.ValidPublicRef(ref) {
					return false
				}
			}
		} else if !protocol.ValidPublicRef(node.Ref) {
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
			!validBoundsForState(node.Bounds) ||
			(node.Axis != "" && !node.Axis.Valid()) {
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

func validBoundsForState(bounds protocol.ReadBounds) bool {
	switch bounds.Kind {
	case protocol.GeometryHidden, protocol.GeometryUnknown, protocol.GeometryPoint:
		return true
	case protocol.GeometryKnown:
		return validBounds(&bounds.Rect)
	default:
		return false
	}
}

func validSetNode(set protocol.ReadSetNode) bool {
	if set.Actions.Set && set.Actions.Value != nil {
		for _, action := range set.Actions.Value {
			if !action.Valid() {
				return false
			}
		}
	}
	if set.Bounds.Set && !validBoundsForState(set.Bounds.Value) {
		return false
	}
	if set.Axis.Set && set.Axis.Value != nil && !set.Axis.Value.Valid() {
		return false
	}
	return true
}

func validFullTree(tree *protocol.ReadTree) bool {
	return tree != nil && tree.Format == "full" && tree.Scope == protocol.Scope &&
		(tree.View == protocol.ViewCompact || tree.View == protocol.ViewDetailed) &&
		validReadNodeTree(tree.Root, true) && tree.Validate() == nil
}

func validDelta(delta *protocol.ReadDelta) bool {
	if delta == nil || delta.Format != "delta" || !validObservationID(delta.Base) ||
		delta.View != protocol.ViewCompact || delta.Validate() != nil {
		return false
	}
	for ref := range delta.Set {
		if !protocol.ValidPublicRef(ref) || !validSetNode(delta.Set[ref]) {
			return false
		}
	}
	added := make(map[string]bool, len(delta.Add))
	for _, node := range delta.Add {
		if !validDeltaNodeTree(node) || added[node.Ref] {
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
		if !protocol.ValidPublicRef(ref) {
			return false
		}
		for _, child := range children {
			if !protocol.ValidPublicRef(child) {
				return false
			}
		}
	}
	for ref, node := range delta.Replace {
		if !protocol.ValidPublicRef(ref) || !validDeltaNodeTree(node) ||
			node.Ref != ref {
			return false
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

func lessPublicRef(left, right string) bool {
	if len(left) != len(right) {
		return len(left) < len(right)
	}
	return left < right
}
