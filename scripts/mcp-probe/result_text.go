package main

import (
	jsonlegacy "encoding/json"
	"encoding/json/v2"
	"strconv"
	"strings"
)

// decodedTextResult is the probe-local oracle for result v4. It is deliberately
// narrow: it restores the facts this external probe checks, while keeping the
// decoded object available to prove that the readable projection preserves
// every non-omitted member.
type decodedTextResult struct {
	view    resultView
	isError bool
	object  map[string]any
	header  resultHeader
}

// treeBaseBinding defers the observation-owned check until every observation
// group has been read. A short delta base therefore resolves against the final
// full O from the same result, even when the id field follows the tree block.
type treeBaseBinding struct {
	rawBase string
}

func decodeTreeBlock(lines []string, start int, object map[string]any,
	seen map[string]bool, bindings *[]treeBaseBinding) (int, *checkError) {
	if failure := requireBlockSeparator(lines, start, "text_tree_separator_invalid"); failure != nil {
		return 0, failure
	}
	format := ""
	rawBase := ""
	expectedNodes := 0
	expectedChanges := 0
	var failure *checkError
	switch {
	case strings.HasPrefix(lines[start], "tree v4 full nodes="):
		format = treeFull
		rawNodes := strings.TrimPrefix(lines[start], "tree v4 full nodes=")
		if expectedNodes, failure = parseCanonicalPositive(rawNodes, "text_tree_count_invalid", "nodes"); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(lines[start], "tree v4 delta base="):
		format = treeDelta
		rest := strings.TrimPrefix(lines[start], "tree v4 delta base=")
		separator := strings.LastIndex(rest, " changes=")
		if separator <= 0 {
			return 0, failf("text_tree_header_invalid")
		}
		rawBase = rest[:separator]
		if _, failure = parseTreeBase(rawBase); failure != nil {
			return 0, failure
		}
		if expectedChanges, failure = parseCanonicalNatural(
			rest[separator+len(" changes="):], "text_tree_count_invalid", "changes"); failure != nil {
			return 0, failure
		}
	default:
		return 0, failf("text_tree_header_invalid")
	}
	if failure := setDecodedPath(object, seen, []string{"observation", "tree", "format"}, format); failure != nil {
		return 0, failure
	}
	if failure := setDecodedPath(object, seen,
		[]string{"observation", "tree", "view"}, "compact"); failure != nil {
		return 0, failure
	}
	if format == treeFull {
		if failure := setDecodedPath(object, seen,
			[]string{"observation", "tree", "scope"}, "reported_visible_with_context"); failure != nil {
			return 0, failure
		}
	}
	end, failure := findBlockEnd(lines, start+2, "end tree", "text_tree_unterminated")
	if failure != nil {
		return 0, failure
	}
	rules := treeBodyRules{
		kind:             format,
		prefix:           "text_tree",
		indentWidth:      1,
		v4:               true,
		rootRoleRequired: format == treeFull,
	}
	stats, failure := analyzeTreeBody(lines[start+2:end-1], rules)
	if failure != nil {
		return 0, failure
	}
	switch format {
	case treeFull:
		if stats.nodes != expectedNodes {
			return 0, failf("text_tree_count_mismatch",
				field("count", "nodes"),
				field("header", strconv.Itoa(expectedNodes)),
				field("body", strconv.Itoa(stats.nodes)))
		}
	case treeDelta:
		if stats.changes != expectedChanges {
			return 0, failf("text_tree_count_mismatch",
				field("count", "changes"),
				field("header", strconv.Itoa(expectedChanges)),
				field("body", strconv.Itoa(stats.changes)))
		}
	}
	*bindings = append(*bindings, treeBaseBinding{rawBase: rawBase})
	return end, nil
}

func resolveTreeBindings(object map[string]any, seen map[string]bool,
	observationIDPresent bool, bindings []treeBaseBinding) *checkError {
	if len(bindings) == 0 {
		return nil
	}
	observationID, ok := lookupPath(object, []string{"observation", "id"})
	currentID, valid := observationID.(string)
	if !ok || !valid || !validTreeTextO(currentID) {
		return failf("text_tree_id_missing")
	}
	if !observationIDPresent {
		return failf("text_tree_id_group_required")
	}
	for _, binding := range bindings {
		if binding.rawBase == "" {
			continue
		}
		base, failure := resolveTreeBase(currentID, binding.rawBase)
		if failure != nil {
			return failure
		}
		if failure := setDecodedPath(object, seen,
			[]string{"observation", "tree", "base"}, base); failure != nil {
			return failure
		}
	}
	return nil
}

func parseTreeBase(value string) (string, *checkError) {
	if strings.HasPrefix(value, "_") {
		sequence := strings.TrimPrefix(value, "_")
		if _, failure := parseCanonicalPositiveUint64(sequence, "text_tree_base_invalid", "base"); failure != nil {
			return "", failure
		}
		return value, nil
	}
	if !validTreeTextO(value) {
		return "", failf("text_tree_base_invalid")
	}
	return value, nil
}

func resolveTreeBase(currentID, rawBase string) (string, *checkError) {
	if !strings.HasPrefix(rawBase, "_") {
		if !validTreeTextO(rawBase) {
			return "", failf("text_tree_base_invalid")
		}
		return rawBase, nil
	}
	if !validTreeTextO(currentID) {
		return "", failf("text_tree_base_current_invalid")
	}
	underscore := strings.LastIndexByte(currentID, '_')
	if underscore <= 1 || underscore == len(currentID)-1 {
		return "", failf("text_tree_base_current_invalid")
	}
	if _, failure := parseCanonicalPositiveUint64(
		currentID[underscore+1:], "text_tree_base_current_invalid", "current"); failure != nil {
		return "", failure
	}
	base := currentID[:underscore+1] + strings.TrimPrefix(rawBase, "_")
	if !validTreeTextO(base) {
		return "", failf("text_tree_base_expanded_invalid")
	}
	return base, nil
}

// validTreeTextO applies the v4 full-O token grammar, not the narrower public
// P/O argument alphabet. The first byte names an observation, the remainder is
// one or more printable ASCII token characters and the complete value stays
// within the public reference length limit.
func validTreeTextO(value string) bool {
	if len(value) < 2 || len(value) > 64 || value[0] != 'O' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character == 0x21,
			character >= 0x23 && character <= 0x3c,
			character >= 0x3e && character <= 0x5b,
			character >= 0x5d && character <= 0x7e:
		default:
			return false
		}
	}
	return true
}

// requireBlockSeparator verifies the single blank line between a readable block
// header and its body. A legacy defaults line is not consumed.
func requireBlockSeparator(lines []string, start int, reason string) *checkError {
	if start+1 >= len(lines) || lines[start+1] != "" {
		return failf(reason)
	}
	return nil
}

func decodeInspectionBlock(lines []string, start int, object map[string]any,
	seen map[string]bool) (int, *checkError) {
	fields, failure := decodeBlockHeader(lines[start], "inspect v3 ")
	if failure != nil {
		return 0, failure
	}
	observationID := fields["observation"]
	if observationID == "" {
		return 0, failf("text_inspect_observation_missing")
	}
	scope := fields["scope"]
	if scope != "tree" && scope != "subtree" && scope != "query" {
		return 0, failf("text_inspect_scope_invalid")
	}
	if fields["view"] != "detailed" {
		return 0, failf("text_inspect_view_invalid")
	}
	revision, ok := parseInteger(fields["revision"])
	if !ok {
		return 0, failf("text_inspect_revision_invalid")
	}
	for key, value := range map[string]any{
		"observation_id": observationID,
		"scope":          scope,
		"revision":       revision,
	} {
		if failure := setDecodedPath(object, seen, []string{"inspection", key}, value); failure != nil {
			return 0, failure
		}
	}
	if capturedAt := fields["captured_at"]; capturedAt != "" {
		if failure := setDecodedPath(object, seen,
			[]string{"inspection", "captured_at"}, capturedAt); failure != nil {
			return 0, failure
		}
	}
	if failure := requireBlockSeparator(lines, start, "text_inspect_separator_invalid"); failure != nil {
		return 0, failure
	}
	expectedNodes, failure := treeHeaderCount(fields, "nodes", "text_inspect")
	if failure != nil {
		return 0, failure
	}
	expectedEntries, failure := treeHeaderCount(fields, "entries", "text_inspect")
	if failure != nil {
		return 0, failure
	}
	expectedMatches := 0
	if scope == "query" {
		if expectedMatches, failure = treeHeaderCount(fields, "matches", "text_inspect"); failure != nil {
			return 0, failure
		}
	}
	// The tree scope declares view in the nested complete tree; the subtree and
	// query scopes declare it once on the inspection envelope.
	viewPath := []string{"inspection", "view"}
	if scope == "tree" {
		viewPath = []string{"inspection", "tree", "view"}
	}
	if failure := setDecodedPath(object, seen, viewPath, fields["view"]); failure != nil {
		return 0, failure
	}
	end, failure := findBlockEnd(lines, start+2, "end inspect", "text_inspect_unterminated")
	if failure != nil {
		return 0, failure
	}
	rules := treeBodyRules{
		kind:               "full",
		prefix:             "text_inspect",
		detailed:           true,
		indentWidth:        2,
		rootRoleRequired:   scope == "tree",
		expectedMatches:    expectedMatches,
		hasExpectedMatches: scope == "query",
	}
	if scope == "query" {
		rules.kind = "query"
	}
	stats, failure := analyzeTreeBody(lines[start+2:end-1], rules)
	if failure != nil {
		return 0, failure
	}
	if failure := compareTreeBodyCount(fields, "nodes", stats.nodes,
		expectedNodes, "text_inspect"); failure != nil {
		return 0, failure
	}
	if failure := compareTreeBodyCount(fields, "entries", stats.entries,
		expectedEntries, "text_inspect"); failure != nil {
		return 0, failure
	}
	if scope == "query" {
		if failure := compareTreeBodyCount(fields, "matches", stats.matches,
			expectedMatches, "text_inspect"); failure != nil {
			return 0, failure
		}
	}
	return end, nil
}

// validateTreeBody is the narrow helper retained for callers that only need a
// structural verdict. Production decoding supplies the detailed/header context
// through analyzeTreeBody so counts and the inspect view are checked together.
func validateTreeBody(body []string, mode, prefix string) *checkError {
	rules := treeBodyRules{kind: mode, prefix: prefix}
	switch mode {
	case "full":
		rules.rootRoleRequired = true
	case "query":
		rules.detailed = true
	}
	_, failure := analyzeTreeBody(body, rules)
	return failure
}

type treeBodyRules struct {
	kind               string
	prefix             string
	detailed           bool
	indentWidth        int
	v4                 bool
	rootRoleRequired   bool
	expectedMatches    int
	hasExpectedMatches bool
}

type treeBodyStats struct {
	nodes   int
	entries int
	changes int
	matches int
}

type treeBodyLine struct {
	depth int
	text  string
	blank bool
}

type treeNodeRules struct {
	prefix           string
	allowChain       bool
	requireGeometry  bool
	rootRoleRequired bool
	v4               bool
	detailed         bool
	ancestorsKnown   []bool
	requireRectangle bool
	explicitActions  bool
}

type treeNodeValidation struct {
	refs        int
	boundsKnown bool
}

func analyzeTreeBody(body []string, rules treeBodyRules) (treeBodyStats, *checkError) {
	lines := make([]treeBodyLine, len(body))
	for index, raw := range body {
		line, failure := parseTreeBodyLine(raw, rules)
		if failure != nil {
			return treeBodyStats{}, failure
		}
		lines[index] = line
	}
	switch rules.kind {
	case "full":
		nodes, entries, failure := analyzeTreeDefinitionTree(lines, rules)
		return treeBodyStats{nodes: nodes, entries: entries}, failure
	case "delta":
		return analyzeTreeDeltaBody(lines, rules)
	case "query":
		return analyzeTreeQueryBody(lines, rules)
	default:
		return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
	}
}

func parseTreeBodyLine(raw string, rules treeBodyRules) (treeBodyLine, *checkError) {
	prefix := rules.prefix
	if strings.ContainsRune(raw, '\t') || strings.TrimRight(raw, " ") != raw {
		return treeBodyLine{}, failf(prefix + "_body_invalid")
	}
	indent := 0
	for indent < len(raw) && raw[indent] == ' ' {
		indent++
	}
	if indent == len(raw) {
		if indent != 0 {
			return treeBodyLine{}, failf(prefix + "_body_invalid")
		}
		return treeBodyLine{blank: true}, nil
	}
	indentWidth := rules.indentWidth
	if indentWidth == 0 {
		indentWidth = 2
	}
	if indent%indentWidth != 0 {
		return treeBodyLine{}, failf(prefix + "_body_invalid")
	}
	return treeBodyLine{depth: indent / indentWidth, text: raw[indent:]}, nil
}

// analyzeTreeDefinitionTree validates one full/compact or detailed subtree.
// Only the first line is a root. Later depth-zero lines are siblings without a
// parent and therefore invalid.
func analyzeTreeDefinitionTree(lines []treeBodyLine,
	rules treeBodyRules) (int, int, *checkError) {
	if len(lines) == 0 {
		return 0, 0, failf(rules.prefix + "_body_invalid")
	}
	nodes, entries := 0, 0
	previousDepth := 0
	ancestorsKnown := make([]bool, 0)
	for index, line := range lines {
		if line.blank {
			return 0, 0, failf(rules.prefix + "_body_invalid")
		}
		if index == 0 {
			if line.depth != 0 {
				return 0, 0, failf(rules.prefix + "_body_invalid")
			}
		} else if line.depth == 0 || line.depth > previousDepth+1 {
			return 0, 0, failf(rules.prefix + "_body_invalid")
		}
		validated, failure := validateTreeDefinition(line.text, treeNodeRules{
			prefix:           rules.prefix,
			allowChain:       !rules.detailed && index > 0,
			requireGeometry:  rules.detailed,
			rootRoleRequired: index == 0 && rules.rootRoleRequired,
			v4:               rules.v4,
			detailed:         rules.detailed,
			ancestorsKnown:   ancestorsKnown[:line.depth],
		})
		if failure != nil {
			return 0, 0, failure
		}
		nodes += validated.refs
		entries++
		ancestorsKnown = append(ancestorsKnown[:line.depth], validated.boundsKnown)
		previousDepth = line.depth
	}
	return nodes, entries, nil
}

func analyzeTreeDeltaBody(lines []treeBodyLine,
	rules treeBodyRules) (treeBodyStats, *checkError) {
	stats := treeBodyStats{}
	previousDepth := 0
	subtreeOpen := false
	ancestorsKnown := make([]bool, 0)
	for _, line := range lines {
		if line.blank {
			return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
		}
		tokens, ok := treeLineTokens(line.text)
		if !ok || len(tokens) == 0 || tokens[0] == "defaults" {
			return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
		}
		if line.depth == 0 {
			stats.changes++
			subtreeOpen = false
			ancestorsKnown = ancestorsKnown[:0]
			switch tokens[0] {
			case "~":
				if len(tokens) < 2 || !isPublicRef(tokens[1]) {
					return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
				}
				if failure := validateSetNodeFlags(tokens[2:], rules.prefix); failure != nil {
					return treeBodyStats{}, failure
				}
			case "+", "=":
				if len(tokens) < 2 {
					return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
				}
				validated, failure := validateTreeNodeTokens(tokens[1:], treeNodeRules{
					prefix:         rules.prefix,
					v4:             rules.v4,
					ancestorsKnown: nil,
				})
				if failure != nil {
					return treeBodyStats{}, failure
				}
				ancestorsKnown = append(ancestorsKnown, validated.boundsKnown)
				subtreeOpen = true
				previousDepth = 0
			case "-":
				if len(tokens) != 2 || !isPublicRef(tokens[1]) {
					return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
				}
			case "children":
				if len(tokens) != 4 || !isPublicRef(tokens[1]) || tokens[2] != "=" ||
					!validTreeRefListToken(tokens[3], 0) {
					return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
				}
			default:
				return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
			}
			continue
		}
		if !subtreeOpen || line.depth > previousDepth+1 {
			return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
		}
		validated, failure := validateTreeDefinition(line.text, treeNodeRules{
			prefix:         rules.prefix,
			allowChain:     true,
			v4:             rules.v4,
			ancestorsKnown: ancestorsKnown[:line.depth],
		})
		if failure != nil {
			return treeBodyStats{}, failure
		}
		ancestorsKnown = append(ancestorsKnown[:line.depth], validated.boundsKnown)
		previousDepth = line.depth
	}
	return stats, nil
}

func analyzeTreeQueryBody(lines []treeBodyLine,
	rules treeBodyRules) (treeBodyStats, *checkError) {
	stats := treeBodyStats{}
	matchNumber := 0
	declaredMatches := 0
	for index := 0; index < len(lines); {
		line := lines[index]
		if line.blank || line.depth != 0 || !strings.HasPrefix(line.text, "match ") {
			return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
		}
		matchNumber++
		total, failure := validateTreeMatchLine(line.text, matchNumber, rules)
		if failure != nil {
			return treeBodyStats{}, failure
		}
		if matchNumber == 1 {
			declaredMatches = total
		} else if declaredMatches != total {
			return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
		}
		index++
		start := index
		for index < len(lines) {
			if lines[index].blank {
				break
			}
			if lines[index].depth == 0 && strings.HasPrefix(lines[index].text, "match ") {
				break
			}
			index++
		}
		if index == start {
			return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
		}
		nodes, entries, failure := analyzeTreeDefinitionTree(lines[start:index], treeBodyRules{
			kind:     "full",
			prefix:   rules.prefix,
			detailed: true,
		})
		if failure != nil {
			return treeBodyStats{}, failure
		}
		stats.nodes += nodes
		stats.entries += entries
		if index < len(lines) {
			if !lines[index].blank {
				return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
			}
			index++
			if index >= len(lines) || lines[index].blank ||
				lines[index].depth != 0 ||
				!strings.HasPrefix(lines[index].text, "match ") {
				return treeBodyStats{}, failf(rules.prefix + "_body_invalid")
			}
		}
	}
	stats.matches = matchNumber
	if declaredMatches != matchNumber {
		return treeBodyStats{}, failf(rules.prefix+"_count_mismatch",
			field("count", "matches"),
			field("header", strconv.Itoa(declaredMatches)),
			field("body", strconv.Itoa(matchNumber)))
	}
	expected := declaredMatches
	if rules.hasExpectedMatches {
		expected = rules.expectedMatches
	}
	if expected != matchNumber {
		return treeBodyStats{}, failf(rules.prefix+"_count_mismatch",
			field("count", "matches"),
			field("header", strconv.Itoa(expected)),
			field("body", strconv.Itoa(matchNumber)))
	}
	return stats, nil
}

func validateTreeMatchLine(text string, number int,
	rules treeBodyRules) (int, *checkError) {
	rest, found := strings.CutPrefix(text, "match ")
	if !found {
		return 0, failf(rules.prefix + "_body_invalid")
	}
	separator := strings.IndexByte(rest, ' ')
	if separator <= 0 {
		return 0, failf(rules.prefix + "_body_invalid")
	}
	position := rest[:separator]
	rest = rest[separator+1:]
	parts := strings.Split(position, "/")
	if len(parts) != 2 {
		return 0, failf(rules.prefix + "_body_invalid")
	}
	current, currentOK := parsePositiveDecimal(parts[0])
	total, totalOK := parsePositiveDecimal(parts[1])
	if !currentOK || !totalOK || current != number || total < number {
		return 0, failf(rules.prefix + "_body_invalid")
	}
	path, found := strings.CutPrefix(rest, "path=[")
	if !found || !strings.HasSuffix(path, "]") {
		return 0, failf(rules.prefix + "_body_invalid")
	}
	path = strings.TrimSuffix(path, "]")
	for _, ref := range strings.Fields(path) {
		if !isPublicRef(ref) {
			return 0, failf(rules.prefix + "_body_invalid")
		}
	}
	return total, nil
}

func treeHeaderCount(fields map[string]string, key, prefix string) (int, *checkError) {
	value, ok := parseNonNegativeDecimal(fields[key])
	if !ok {
		return 0, failf(prefix+"_count_invalid", field("count", key))
	}
	return value, nil
}

func parseCanonicalPositive(value, prefix, count string) (int, *checkError) {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return 0, failf(prefix, field("count", count))
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return 0, failf(prefix, field("count", count))
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, failf(prefix, field("count", count))
	}
	return parsed, nil
}

func parseCanonicalNatural(value, prefix, count string) (int, *checkError) {
	if value == "0" {
		return 0, nil
	}
	return parseCanonicalPositive(value, prefix, count)
}

func parseCanonicalPositiveUint64(value, prefix, count string) (uint64, *checkError) {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return 0, failf(prefix, field("count", count))
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return 0, failf(prefix, field("count", count))
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, failf(prefix, field("count", count))
	}
	return parsed, nil
}

func compareTreeBodyCount(fields map[string]string, key string, actual,
	expected int, prefix string) *checkError {
	if actual == expected {
		return nil
	}
	return failf(prefix+"_count_mismatch",
		field("count", key),
		field("header", fields[key]),
		field("body", strconv.Itoa(actual)))
}

func parseNonNegativeDecimal(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil && parsed >= 0
}

func parsePositiveDecimal(value string) (int, bool) {
	parsed, ok := parseNonNegativeDecimal(value)
	return parsed, ok && parsed > 0
}

func validateTreeDefinition(text string, rules treeNodeRules) (treeNodeValidation, *checkError) {
	tokens, ok := treeLineTokens(text)
	if !ok {
		return treeNodeValidation{}, failf(rules.prefix + "_body_invalid")
	}
	return validateTreeNodeTokens(tokens, rules)
}

func validateTreeNodeTokens(tokens []string, rules treeNodeRules) (treeNodeValidation, *checkError) {
	if len(tokens) == 0 || tokens[0] == "defaults" {
		return treeNodeValidation{}, failf(rules.prefix + "_body_invalid")
	}
	if failure := validateFullNodeFlags(tokens, rules.prefix); failure != nil {
		return treeNodeValidation{}, failure
	}
	if tokens[0] == "chain" {
		if !rules.allowChain || rules.requireGeometry || len(tokens) < 3 ||
			!validTreeRefListToken(tokens[1], 2) || !treeRoleSet[tokens[2]] {
			return treeNodeValidation{}, failf(rules.prefix + "_body_invalid")
		}
		columnRules := rules
		columnRules.requireRectangle = rules.v4
		columnRules.explicitActions = true
		validated, failure := validateTreeNodeColumns(tokens, 3, true,
			tokens[2], columnRules)
		if failure != nil {
			return treeNodeValidation{}, failure
		}
		refs, _ := parseTreeRefListToken(tokens[1])
		validated.refs = len(refs)
		return validated, nil
	}
	if strings.HasPrefix(tokens[0], "[") {
		if rules.v4 || !rules.allowChain || rules.requireGeometry || rules.rootRoleRequired ||
			len(tokens) != 1 || !validTreeRefListToken(tokens[0], 2) {
			return treeNodeValidation{}, failf(rules.prefix + "_body_invalid")
		}
		refs, _ := parseTreeRefListToken(tokens[0])
		return treeNodeValidation{refs: len(refs)}, nil
	}
	if !isPublicRef(tokens[0]) {
		return treeNodeValidation{}, failf(rules.prefix + "_body_invalid")
	}
	role := "view"
	columnStart := 1
	allowGeometry := true
	if columnStart < len(tokens) && treeRoleSet[tokens[columnStart]] {
		role = tokens[columnStart]
		columnStart++
	} else if columnStart < len(tokens) && isTreeJSONString(tokens[columnStart]) {
		role = "text"
		allowGeometry = false
	}
	if rules.rootRoleRequired && role != "root" {
		return treeNodeValidation{}, failf(rules.prefix + "_body_invalid")
	}
	if rules.detailed {
		rules.explicitActions = true
	}
	validated, failure := validateTreeNodeColumns(tokens, columnStart, allowGeometry,
		role, rules)
	if failure != nil {
		return treeNodeValidation{}, failure
	}
	validated.refs = 1
	return validated, nil
}

func validateTreeNodeColumns(tokens []string, start int, allowGeometry bool,
	role string, rules treeNodeRules) (treeNodeValidation, *checkError) {
	prefix := rules.prefix
	index := start
	geometryKnown := false
	haveGeometry := false
	if allowGeometry && index < len(tokens) {
		switch {
		case tokens[index] == "@":
			if !rules.v4 || !nearestKnownAncestor(rules.ancestorsKnown) {
				return treeNodeValidation{}, failf(prefix + "_body_invalid")
			}
			geometryKnown = true
			haveGeometry = true
			index++
		case tokens[index] == "-":
			haveGeometry = true
			index++
		case rules.v4 && looksLikeUnparenthesizedRectangle(tokens[index]):
			if !validV4TreeGeometry(tokens[index]) {
				return treeNodeValidation{}, failf(prefix + "_body_invalid")
			}
			geometryKnown = true
			haveGeometry = true
			index++
		case !rules.v4 && strings.HasPrefix(tokens[index], "["):
			if !validDetailedTreeGeometry(tokens[index]) {
				return treeNodeValidation{}, failf(prefix + "_body_invalid")
			}
			geometryKnown = true
			haveGeometry = true
			index++
		}
	}
	if rules.requireGeometry && !haveGeometry {
		return treeNodeValidation{}, failf(prefix + "_body_invalid")
	}
	if rules.requireRectangle &&
		(!haveGeometry || !geometryKnown || strings.HasPrefix(tokens[start], "@")) {
		return treeNodeValidation{}, failf(prefix + "_body_invalid")
	}
	for index < len(tokens) && isTreeJSONString(tokens[index]) {
		index++
	}
	if index < len(tokens) && strings.HasPrefix(tokens[index], "value=") {
		if !isTreeJSONString(strings.TrimPrefix(tokens[index], "value=")) {
			return treeNodeValidation{}, failf(prefix + "_body_invalid")
		}
		index++
	}
	if index < len(tokens) && strings.HasPrefix(tokens[index], "hint=") {
		if !isTreeJSONString(strings.TrimPrefix(tokens[index], "hint=")) {
			return treeNodeValidation{}, failf(prefix + "_body_invalid")
		}
		index++
	}
	explicitActions := make(map[string]bool)
	defaultAction := treeRoleDefaultAction[role]
	negatedAction := ""
	actions := make([]string, 0, len(treeActionSet))
	for index < len(tokens) {
		action := tokens[index]
		if treeActionSet[action] {
			if negatedAction != "" {
				return treeNodeValidation{}, failf(prefix + "_body_invalid")
			}
			explicitActions[action] = true
			actions = append(actions, action)
			index++
			continue
		}
		if strings.HasPrefix(action, "!") {
			name := strings.TrimPrefix(action, "!")
			if !rules.v4 || rules.explicitActions || name != defaultAction || negatedAction != "" ||
				explicitActions[name] {
				return treeNodeValidation{}, failf(prefix + "_body_invalid")
			}
			negatedAction = name
			index++
			continue
		}
		break
	}
	if rules.v4 && negatedAction == "" {
		lastAction := -1
		for _, action := range actions {
			order := treeActionOrder[action]
			if order < lastAction {
				return treeNodeValidation{}, failf(prefix + "_body_invalid")
			}
			lastAction = order
		}
	}
	lastFlag := -1
	for index < len(tokens) {
		name, _, found := strings.Cut(tokens[index], "=")
		order, known := treeFullFlagOrder[name]
		if !found || !known {
			break
		}
		if order <= lastFlag {
			return treeNodeValidation{}, failf(prefix + "_body_invalid")
		}
		lastFlag = order
		index++
	}
	if index < len(tokens) && strings.HasPrefix(tokens[index], "axis=") {
		axis := strings.TrimPrefix(tokens[index], "axis=")
		if axis != "x" && axis != "y" && axis != "both" {
			return treeNodeValidation{}, failf(prefix + "_body_invalid")
		}
		index++
	}
	if index < len(tokens) && strings.HasPrefix(tokens[index], "resource=") {
		if !isTreeJSONString(strings.TrimPrefix(tokens[index], "resource=")) {
			return treeNodeValidation{}, failf(prefix + "_body_invalid")
		}
		index++
	}
	if index < len(tokens) && tokens[index] == "context" {
		index++
	}
	if index != len(tokens) {
		return treeNodeValidation{}, failf(prefix + "_body_invalid")
	}
	return treeNodeValidation{boundsKnown: geometryKnown}, nil
}

var treeFullFlagOrder = map[string]int{
	"enabled": 0, "focused": 1, "selected": 2, "protected": 3, "checked": 4,
}

var treeRoleSet = map[string]bool{
	"root": true, "group": true, "text": true, "button": true,
	"input": true, "checkbox": true, "radio": true, "switch": true,
	"toggle": true, "scroll": true, "image": true, "view": true,
}

var treeActionSet = map[string]bool{
	"tap": true, "long_press": true, "text": true, "swipe": true,
}

var treeActionOrder = map[string]int{
	"tap": 0, "long_press": 1, "text": 2, "swipe": 3,
}

var treeRoleDefaultAction = map[string]string{
	"button": "tap",
	"input":  "text",
	"scroll": "swipe",
}

func nearestKnownAncestor(ancestorsKnown []bool) bool {
	for index := len(ancestorsKnown) - 1; index >= 0; index-- {
		if ancestorsKnown[index] {
			return true
		}
	}
	return false
}

func looksLikeUnparenthesizedRectangle(token string) bool {
	return strings.Count(token, ",") == 3
}

func validV4TreeGeometry(token string) bool {
	parts := strings.Split(token, ",")
	if len(parts) != 4 {
		return false
	}
	values := [4]int{}
	for index, part := range parts {
		value, ok := parseCanonicalTreeInteger(part)
		if !ok {
			return false
		}
		values[index] = value
	}
	return values[2] >= values[0] && values[3] >= values[1]
}

func validDetailedTreeGeometry(token string) bool {
	if token == "-" {
		return true
	}
	if len(token) < 2 || token[0] != '[' || token[len(token)-1] != ']' {
		return false
	}
	parts := strings.Split(token[1:len(token)-1], ",")
	if len(parts) != 4 {
		return false
	}
	values := [4]int{}
	for index, part := range parts {
		value, ok := parseCanonicalTreeInteger(part)
		if !ok {
			return false
		}
		values[index] = value
	}
	return values[2] >= values[0] && values[3] >= values[1]
}

func parseCanonicalTreeInteger(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	digits := value
	negative := false
	if digits[0] == '-' {
		digits = digits[1:]
		negative = true
	}
	if digits == "" || (negative && digits == "0") ||
		(digits[0] == '0' && len(digits) > 1) {
		return 0, false
	}
	for index := 0; index < len(digits); index++ {
		if digits[index] < '0' || digits[index] > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil
}

func validTreeRefListToken(token string, minimum int) bool {
	refs, ok := parseTreeRefListToken(token)
	return ok && len(refs) >= minimum
}

func parseTreeRefListToken(token string) ([]string, bool) {
	if len(token) < 2 || token[0] != '[' || token[len(token)-1] != ']' {
		return nil, false
	}
	refs := strings.Fields(token[1 : len(token)-1])
	for _, ref := range refs {
		if !isPublicRef(ref) {
			return nil, false
		}
	}
	return refs, true
}

func isPublicRef(ref string) bool {
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

func isTreeJSONString(token string) bool {
	if len(token) < 2 || token[0] != '"' || token[len(token)-1] != '"' {
		return false
	}
	value, failure := decodeJSONValue(token)
	if failure != nil {
		return false
	}
	_, ok := value.(string)
	return ok
}

// validateFullNodeFlags rejects state members whose value is the complete-node
// default. Unknown is null and therefore explicit; the non-default known value
// is explicit. This is the contract that allows the removed defaults line to be
// omitted without letting an old producer silently keep writing it per node.
func validateFullNodeFlags(tokens []string, prefix string) *checkError {
	for _, token := range tokens {
		name, value, found := strings.Cut(token, "=")
		if !found {
			continue
		}
		switch name {
		case "enabled":
			if value == "true" {
				return failf(prefix+"_flag_default", field("flag", name))
			}
			if value != "false" && value != "null" {
				return failf(prefix+"_flag_invalid", field("flag", name))
			}
		case "focused", "selected", "protected", "checked":
			if value == "false" {
				return failf(prefix+"_flag_default", field("flag", name))
			}
			if value != "true" && value != "null" {
				return failf(prefix+"_flag_invalid", field("flag", name))
			}
		}
	}
	return nil
}

// validateSetNodeFlags accepts every explicit tri-state value. An absent member
// alone means "not modified".
func validateSetNodeFlags(tokens []string, prefix string) *checkError {
	for _, token := range tokens {
		name, value, found := strings.Cut(token, "=")
		if !found {
			continue
		}
		switch name {
		case "enabled", "focused", "selected", "protected", "checked":
			if value != "true" && value != "false" && value != "null" {
				return failf(prefix+"_flag_invalid", field("flag", name))
			}
		}
	}
	return nil
}

// treeLineTokens splits one node line while keeping quoted JSON strings and
// bracketed arrays as single tokens. A ref must write none of them, so this
// small scanner is sufficient to find state members without being confused by a
// label such as "checked=false".
func treeLineTokens(line string) ([]string, bool) {
	var tokens []string
	for index := 0; index < len(line); {
		for index < len(line) && line[index] == ' ' {
			index++
		}
		if index >= len(line) {
			break
		}
		start := index
		for index < len(line) && line[index] != ' ' {
			switch line[index] {
			case '"':
				end, ok := treeJSONStringEnd(line, index)
				if !ok {
					return nil, false
				}
				index = end
			case '[':
				end, ok := treeBracketEnd(line, index)
				if !ok {
					return nil, false
				}
				index = end
			default:
				index++
			}
		}
		tokens = append(tokens, line[start:index])
	}
	return tokens, true
}

func treeJSONStringEnd(value string, start int) (int, bool) {
	escaped := false
	for index := start + 1; index < len(value); index++ {
		switch {
		case escaped:
			escaped = false
		case value[index] == '\\':
			escaped = true
		case value[index] == '"':
			return index + 1, true
		}
	}
	return 0, false
}

func treeBracketEnd(value string, start int) (int, bool) {
	depth := 0
	for index := start; index < len(value); index++ {
		switch value[index] {
		case '"':
			end, ok := treeJSONStringEnd(value, index)
			if !ok {
				return 0, false
			}
			index = end - 1
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return index + 1, true
			}
		}
	}
	return 0, false
}

func decodeShellBlock(lines []string, start int, object map[string]any,
	seen map[string]bool) (int, *checkError) {
	fields, failure := decodeBlockHeader(lines[start], "shell v1 ")
	if failure != nil {
		return 0, failure
	}
	if fields["termination"] == "" {
		return 0, failf("text_shell_termination_missing")
	}
	exitCode, failure := decodeTextScalar(fields["exit_code"])
	if failure != nil {
		return 0, failure
	}
	truncated, failure := decodeTextScalar(fields["truncated"])
	if failure != nil {
		return 0, failure
	}
	outputBytes, failure := decodeTextScalar(fields["output_bytes"])
	if failure != nil {
		return 0, failure
	}
	var output strings.Builder
	var outputError any
	var haveOutputError bool
	end := start + 1
	for ; end < len(lines); end++ {
		line := lines[end]
		switch {
		case line == "end shell":
			if outputBytes == nil {
				if output.Len() != 0 {
					return 0, failf("text_shell_output_with_null_length")
				}
			} else {
				expected, ok := valueInteger(outputBytes)
				if !ok || expected != int64(output.Len()) {
					return 0, failf("text_shell_output_bytes_mismatch")
				}
			}
			for key, value := range map[string]any{
				"termination": fields["termination"],
				"exit_code":   exitCode,
				"output":      nil,
				"truncated":   truncated,
			} {
				if outputBytes != nil {
					if key == "output" {
						value = output.String()
					}
				}
				if failure := setDecodedPath(object, seen, []string{"shell", key}, value); failure != nil {
					return 0, failure
				}
			}
			if haveOutputError {
				if failure := setDecodedPath(object, seen,
					[]string{"shell", "output_error"}, outputError); failure != nil {
					return 0, failure
				}
			}
			return end + 1, nil
		case strings.HasPrefix(line, "output "):
			chunk, failure := decodeJSONValue(line[len("output "):])
			if failure != nil {
				return 0, failure
			}
			text, ok := chunk.(string)
			if !ok {
				return 0, failf("text_shell_output_not_string")
			}
			output.WriteString(text)
		case strings.HasPrefix(line, "output_error "):
			value, failure := decodeJSONValue(line[len("output_error "):])
			if failure != nil {
				return 0, failure
			}
			outputError = value
			haveOutputError = true
		case strings.Contains(line, " omitted "):
			return 0, failf("text_result_block_omitted")
		default:
			return 0, failf("text_shell_item_invalid")
		}
	}
	return 0, failf("text_shell_unterminated")
}

func decodeTimelineBlock(lines []string, start int, object map[string]any,
	seen map[string]bool) (int, *checkError) {
	fields, failure := decodeBlockHeader(lines[start], "timeline v1 ")
	if failure != nil {
		return 0, failure
	}
	count, ok := parseInteger(fields["events"])
	if !ok || count < 0 {
		return 0, failf("text_timeline_count_invalid")
	}
	events := make([]any, 0, count)
	end := start + 1
	for ; end < len(lines); end++ {
		line := lines[end]
		if line == "end timeline" {
			if len(events) != count {
				return 0, failf("text_timeline_count_mismatch")
			}
			if failure := setDecodedPath(object, seen,
				[]string{"diagnosis", "timeline"}, events); failure != nil {
				return 0, failure
			}
			return end + 1, nil
		}
		if !strings.HasPrefix(line, "event ") {
			return 0, failf("text_timeline_event_invalid")
		}
		event, failure := decodeJSONValue(line[len("event "):])
		if failure != nil {
			return 0, failure
		}
		if _, ok := event.(map[string]any); !ok {
			return 0, failf("text_timeline_event_not_object")
		}
		events = append(events, event)
	}
	return 0, failf("text_timeline_unterminated")
}

func findBlockEnd(lines []string, start int, marker, reason string) (int, *checkError) {
	for index := start; index < len(lines); index++ {
		if strings.Contains(lines[index], " omitted ") {
			return 0, failf("text_result_block_omitted")
		}
		if lines[index] == marker {
			return index + 1, nil
		}
	}
	return 0, failf(reason)
}

func decodeBlockHeader(line, prefix string) (map[string]string, *checkError) {
	if !strings.HasPrefix(line, prefix) {
		return nil, failf("text_result_block_header_invalid")
	}
	fields := make(map[string]string)
	for _, token := range strings.Fields(strings.TrimPrefix(line, prefix)) {
		key, value, found := strings.Cut(token, "=")
		if !found || key == "" || value == "" {
			return nil, failf("text_result_block_field_invalid")
		}
		if _, exists := fields[key]; exists {
			return nil, failf("text_result_block_field_duplicate")
		}
		fields[key] = value
	}
	return fields, nil
}

// appTextFieldKind is the probe-side classification of every app member that
// must use a typed readable line. Composite members stay complete compact JSON,
// never a synthesized count.
type appTextFieldKind uint8

const (
	appTextFieldToken appTextFieldKind = iota + 1
	appTextFieldString
	appTextFieldInt
	appTextFieldBool
	appTextFieldNullableBool
	appTextFieldArray
	appTextFieldIntArray
	appTextFieldObject
)

var appTextFields = map[string]appTextFieldKind{
	"kind":               appTextFieldToken,
	"ownership":          appTextFieldToken,
	"foreground_restore": appTextFieldToken,
	"captured_at":        appTextFieldToken,
	"package":            appTextFieldString,
	"task_id":            appTextFieldInt,
	"source_task_id":     appTextFieldInt,
	"display_id":         appTextFieldInt,
	"source_display_id":  appTextFieldInt,
	"total":              appTextFieldInt,
	"reused":             appTextFieldBool,
	"state_preserved":    appTextFieldNullableBool,
	"complete":           appTextFieldBool,
	"truncated":          appTextFieldBool,
	"restarted":          appTextFieldNullableBool,
	"items":              appTextFieldArray,
	"tasks":              appTextFieldArray,
	"foreground":         appTextFieldObject,
	"target_pids":        appTextFieldIntArray,
	"killed_pids":        appTextFieldIntArray,
	"remaining_pids":     appTextFieldIntArray,
}

var (
	appKindSet = map[string]bool{
		"find": true, "list": true, "open": true, "claim": true,
		"handoff": true, "close": true, "kill": true,
	}
	appOwnershipSet = map[string]bool{
		"owned": true, "borrowed": true, "shared": true,
	}
	appForegroundRestoreSet = map[string]bool{
		"not_requested": true, "confirmed": true, "unconfirmed": true,
	}
)

// isTypedAppPath reports whether a json v1 fallback pointer names an app member
// that the contract requires on its typed line. Unknown app members still fall
// back whole, as do unknown fields of every other object.
func isTypedAppPath(pointer string) bool {
	parts := strings.Split(pointer, "/")
	if len(parts) < 2 || parts[0] != "" || parts[1] != "app" {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	// A collection block owns the array; an unknown member of one concrete
	// element is legitimately carried by a json v1 fallback.
	if len(parts) >= 4 && (parts[2] == "items" || parts[2] == "tasks") {
		return false
	}
	_, known := appTextFields[parts[2]]
	return known
}

// applyAppTextField validates one app.kind=... style line and stores it. A known
// member may never arrive through json v1, and an unknown member may never take
// a typed line: both would let one channel hide a field the other carries.
func applyAppTextField(object map[string]any, seen map[string]bool,
	key, rawValue string) *checkError {
	name := strings.TrimPrefix(key, "app.")
	kind, known := appTextFields[name]
	if !known {
		return failf("text_result_app_field_unknown", field("field", key))
	}
	value, failure := decodeTextScalar(rawValue)
	if failure != nil {
		return failure
	}
	if failure := validateAppTextValue(name, kind, rawValue, value); failure != nil {
		return failure
	}
	return setDecodedPath(object, seen, []string{"app", name}, value)
}

func validateAppTextValue(name string, kind appTextFieldKind, rawValue string, value any) *checkError {
	switch kind {
	case appTextFieldToken:
		text, ok := value.(string)
		if !ok || text == "" || strings.HasPrefix(rawValue, `"`) {
			return failf("text_result_app_token_invalid", field("field", name))
		}
		switch name {
		case "kind":
			if !appKindSet[text] {
				return failf("text_result_app_kind_invalid", field("kind", text))
			}
		case "ownership":
			if !appOwnershipSet[text] {
				return failf("text_result_app_ownership_invalid", field("ownership", text))
			}
		case "foreground_restore":
			if !appForegroundRestoreSet[text] {
				return failf("text_result_app_foreground_restore_invalid", field("state", text))
			}
		}
	case appTextFieldString:
		if _, ok := value.(string); !ok || !strings.HasPrefix(rawValue, `"`) {
			return failf("text_result_app_string_invalid", field("field", name))
		}
	case appTextFieldInt:
		number, ok := exactInteger(value)
		if !ok || number < 0 {
			return failf("text_result_app_number_invalid", field("field", name))
		}
		if (name == "task_id" || name == "source_task_id") && number <= 0 {
			return failf("text_result_app_number_invalid", field("field", name))
		}
	case appTextFieldBool:
		if _, ok := value.(bool); !ok {
			return failf("text_result_app_bool_invalid", field("field", name))
		}
	case appTextFieldNullableBool:
		if value != nil {
			if _, ok := value.(bool); !ok {
				return failf("text_result_app_bool_invalid", field("field", name))
			}
		}
	case appTextFieldArray:
		values, ok := value.([]any)
		if !ok {
			return failf("text_result_app_array_invalid", field("field", name))
		}
		for _, element := range values {
			if _, ok := element.(map[string]any); !ok {
				return failf("text_result_app_array_element_invalid", field("field", name))
			}
		}
	case appTextFieldIntArray:
		values, ok := value.([]any)
		if !ok {
			return failf("text_result_app_int_array_invalid", field("field", name))
		}
		previous := int64(-1)
		for _, element := range values {
			number, ok := exactInteger(element)
			if !ok || number < 0 {
				return failf("text_result_app_int_array_invalid", field("field", name))
			}
			if number < previous {
				return failf("text_result_app_int_array_unsorted", field("field", name))
			}
			previous = number
		}
	case appTextFieldObject:
		if _, ok := value.(map[string]any); !ok {
			return failf("text_result_app_object_invalid", field("field", name))
		}
	}
	return nil
}

func exactInteger(value any) (int64, bool) {
	switch number := value.(type) {
	case int64:
		return number, true
	case int:
		return int64(number), true
	case jsonlegacy.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func decodeJSONFallback(line string) (string, any, *checkError) {
	rest, found := strings.CutPrefix(line, "json v1 path=")
	if !found || !strings.HasPrefix(rest, `"`) {
		return "", nil, failf("text_result_json_fallback_invalid")
	}
	end, ok := endJSONString(rest)
	if !ok {
		return "", nil, failf("text_result_json_path_invalid")
	}
	var path string
	if err := json.Unmarshal([]byte(rest[:end]), &path); err != nil || !strings.HasPrefix(path, "/") {
		return "", nil, failf("text_result_json_path_invalid")
	}
	rest = rest[end:]
	rawValue, found := strings.CutPrefix(rest, " value=")
	if !found {
		return "", nil, failf("text_result_json_fallback_invalid")
	}
	value, failure := decodeJSONValue(rawValue)
	if failure != nil {
		return "", nil, failure
	}
	return path, value, nil
}

func endJSONString(value string) (int, bool) {
	escaped := false
	for index := 1; index < len(value); index++ {
		switch {
		case escaped:
			escaped = false
		case value[index] == '\\':
			escaped = true
		case value[index] == '"':
			return index + 1, true
		}
	}
	return 0, false
}

func decodeTextScalar(value string) (any, *checkError) {
	if value == "" {
		return nil, failf("text_result_value_missing")
	}
	switch value[0] {
	case '"', '{', '[':
		return decodeJSONValue(value)
	}
	if value == "true" || value == "false" || value == "null" {
		return decodeJSONValue(value)
	}
	if integer, err := strconv.ParseInt(value, 10, 64); err == nil {
		return integer, nil
	}
	return value, nil
}

func decodeJSONValue(value string) (any, *checkError) {
	decoded, err := decodeExactJSON([]byte(value))
	if err != nil {
		return nil, failf("text_result_json_invalid")
	}
	return decoded, nil
}

func parseInteger(value string) (int, bool) {
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil
}

func valueInteger(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int64:
		return number, true
	case float64:
		if number != float64(int64(number)) {
			return 0, false
		}
		return int64(number), true
	case jsonlegacy.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func setDecodedPath(object map[string]any, seen map[string]bool, path []string,
	value any) *checkError {
	if len(path) == 0 {
		return failf("text_result_path_invalid")
	}
	for _, part := range path {
		if part == "" {
			return failf("text_result_path_invalid")
		}
	}
	pointer := decodedPathPointer(path)
	if seen[pointer] {
		return failf("text_result_path_duplicate")
	}
	for index := 1; index < len(path); index++ {
		ancestor := decodedPathPointer(path[:index])
		if !seen[ancestor] {
			continue
		}
		// A collection block legitimately owns its array while later fallback
		// lines may add one unknown member under a concrete element. Extending a
		// fully decoded object is still a conflict.
		if value, found := lookupPath(object, path[:index]); found {
			if _, isArray := value.([]any); isArray {
				continue
			}
		}
		return failf("text_result_path_conflict")
	}
	descendantPrefix := pointer + "/"
	for existing := range seen {
		if strings.HasPrefix(existing, descendantPrefix) {
			return failf("text_result_path_conflict")
		}
	}
	_, failure := setNestedValue(object, path, value)
	if failure != nil {
		return failure
	}
	seen[pointer] = true
	return nil
}

func decodedPathPointer(path []string) string {
	return "/" + strings.Join(escapePointerParts(path), "/")
}

func setJSONPointer(object map[string]any, seen map[string]bool, pointer string,
	value any) *checkError {
	parts := strings.Split(pointer, "/")
	if len(parts) < 2 || parts[0] != "" {
		return failf("text_result_pointer_invalid")
	}
	path := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		decoded, ok := unescapePointerPart(part)
		if !ok {
			return failf("text_result_pointer_invalid")
		}
		path = append(path, decoded)
	}
	return setDecodedPath(object, seen, path, value)
}

func setNestedValue(current any, path []string, value any) (any, *checkError) {
	if len(path) == 0 {
		return value, nil
	}
	part := path[0]
	switch node := current.(type) {
	case map[string]any:
		child, exists := node[part]
		if !exists && len(path) > 1 {
			child = make(map[string]any)
		}
		updated, failure := setNestedValue(child, path[1:], value)
		if failure != nil {
			return nil, failure
		}
		node[part] = updated
		return node, nil
	case []any:
		index, ok := parseArrayIndex(part)
		if !ok {
			return nil, failf("text_result_pointer_invalid")
		}
		for len(node) <= index {
			node = append(node, nil)
		}
		updated, failure := setNestedValue(node[index], path[1:], value)
		if failure != nil {
			return nil, failure
		}
		node[index] = updated
		return node, nil
	default:
		return nil, failf("text_result_path_conflict")
	}
}

func parseArrayIndex(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	if value == "0" {
		return 0, true
	}
	if value[0] == '0' {
		return 0, false
	}
	index, err := strconv.Atoi(value)
	return index, err == nil && index > 0
}

func lookupPath(value any, path []string) (any, bool) {
	current := value
	for _, part := range path {
		switch node := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = node[part]
			if !ok {
				return nil, false
			}
		case []any:
			index, ok := parseArrayIndex(part)
			if !ok || index >= len(node) {
				return nil, false
			}
			current = node[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func escapePointerParts(parts []string) []string {
	escaped := make([]string, len(parts))
	for index, part := range parts {
		escaped[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
	}
	return escaped
}

func unescapePointerPart(part string) (string, bool) {
	var builder strings.Builder
	for index := 0; index < len(part); index++ {
		if part[index] != '~' {
			builder.WriteByte(part[index])
			continue
		}
		if index+1 >= len(part) {
			return "", false
		}
		index++
		switch part[index] {
		case '0':
			builder.WriteByte('~')
		case '1':
			builder.WriteByte('/')
		default:
			return "", false
		}
	}
	return builder.String(), true
}
