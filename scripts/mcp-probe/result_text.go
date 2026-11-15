package main

import (
	jsonlegacy "encoding/json"
	"encoding/json/v2"
	"strconv"
	"strings"
)

// decodedTextResult is the probe-local oracle for result v2. It is deliberately
// narrow: it restores the facts this external probe checks, while keeping the
// decoded object available to prove that readable blocks preserve complete
// item/event JSON.
type decodedTextResult struct {
	view    resultView
	isError bool
	object  map[string]any
}

// decodeResultText parses one complete result v2 projection. It rejects a
// missing/repeated envelope, omitted readable blocks and duplicate paths.
func decodeResultText(text string) (*decodedTextResult, *checkError) {
	if strings.Contains(text, "\r") {
		return nil, failf("text_result_naked_cr")
	}
	text = strings.TrimSuffix(text, "\n")
	if text == "" || strings.HasSuffix(text, "\n") {
		return nil, failf("text_result_trailing_blank")
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return nil, failf("text_result_incomplete")
	}
	rawError, found := strings.CutPrefix(lines[0], "result v2 is_error=")
	if !found || (rawError != "true" && rawError != "false") {
		return nil, failf("text_result_header_invalid")
	}
	if lines[len(lines)-1] != "end result" {
		return nil, failf("text_result_unterminated")
	}

	object := make(map[string]any)
	seen := make(map[string]bool)
	for index := 1; index < len(lines)-1; {
		line := lines[index]
		if line == "" {
			if index+1 >= len(lines)-1 || !isResultBlockHeader(lines[index+1]) {
				return nil, failf("text_result_blank_invalid")
			}
			index++
			continue
		}
		if isResultBlockHeader(line) {
			if index > 1 && lines[index-1] != "" {
				return nil, failf("text_result_block_separator_missing")
			}
			next, failure := decodeResultBlock(lines, index, object, seen)
			if failure != nil {
				return nil, failure
			}
			index = next
			continue
		}
		if strings.HasPrefix(line, "json v1 ") {
			path, value, failure := decodeJSONFallback(line)
			if failure != nil {
				return nil, failure
			}
			if isTypedAppPath(path) {
				return nil, failf("text_result_app_field_typed_required")
			}
			if failure := setJSONPointer(object, seen, path, value); failure != nil {
				return nil, failure
			}
			index++
			continue
		}
		key, rawValue, found := strings.Cut(line, "=")
		if !found || key == "" {
			return nil, failf("text_result_item_invalid")
		}
		if strings.HasPrefix(key, "app.") {
			if failure := applyAppTextField(object, seen, key, rawValue); failure != nil {
				return nil, failure
			}
			index++
			continue
		}
		value, failure := decodeTextScalar(rawValue)
		if failure != nil {
			return nil, failure
		}
		path := strings.Split(key, ".")
		if failure := setDecodedPath(object, seen, path, value); failure != nil {
			return nil, failure
		}
		index++
	}

	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, failf("text_result_unusable")
	}
	var view resultView
	if err := json.Unmarshal(encoded, &view); err != nil {
		return nil, failf("text_result_unusable")
	}
	return &decodedTextResult{
		view:    view,
		isError: rawError == "true",
		object:  object,
	}, nil
}

func isResultBlockHeader(line string) bool {
	for _, prefix := range []string{
		"tree v2 ", "inspect v2 ", "shell v1 ", "timeline v1 ",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func decodeResultBlock(lines []string, start int, object map[string]any,
	seen map[string]bool) (int, *checkError) {
	if strings.Contains(lines[start], " omitted ") {
		return 0, failf("text_result_block_omitted")
	}
	switch {
	case strings.HasPrefix(lines[start], "tree v2 "):
		return decodeTreeBlock(lines, start, object, seen)
	case strings.HasPrefix(lines[start], "inspect v2 "):
		return decodeInspectionBlock(lines, start, object, seen)
	case strings.HasPrefix(lines[start], "shell v1 "):
		return decodeShellBlock(lines, start, object, seen)
	case strings.HasPrefix(lines[start], "timeline v1 "):
		return decodeTimelineBlock(lines, start, object, seen)
	default:
		return 0, failf("text_result_block_unknown")
	}
}

func decodeTreeBlock(lines []string, start int, object map[string]any,
	seen map[string]bool) (int, *checkError) {
	fields, failure := decodeBlockHeader(lines[start], "tree v2 ")
	if failure != nil {
		return 0, failure
	}
	format := fields["format"]
	if format != treeFull && format != treeDelta {
		return 0, failf("text_tree_format_invalid")
	}
	id := fields["id"]
	if id == "" {
		return 0, failf("text_tree_id_missing")
	}
	observationID, _ := lookupPath(object, []string{"observation", "id"})
	if text, ok := observationID.(string); !ok || text != id {
		return 0, failf("text_tree_id_mismatch")
	}
	if format == treeDelta && fields["base"] == "" {
		return 0, failf("text_tree_base_missing")
	}
	if format == treeFull && fields["scope"] == "" {
		return 0, failf("text_tree_scope_missing")
	}
	if fields["view"] != "compact" {
		return 0, failf("text_tree_view_invalid")
	}
	if failure := requireBlockSeparator(lines, start, "text_tree_separator_invalid"); failure != nil {
		return 0, failure
	}
	if failure := setDecodedPath(object, seen, []string{"observation", "tree", "format"}, format); failure != nil {
		return 0, failure
	}
	if failure := setDecodedPath(object, seen,
		[]string{"observation", "tree", "view"}, fields["view"]); failure != nil {
		return 0, failure
	}
	if format == treeDelta {
		if failure := setDecodedPath(object, seen, []string{"observation", "tree", "base"}, fields["base"]); failure != nil {
			return 0, failure
		}
	} else {
		if failure := setDecodedPath(object, seen, []string{"observation", "tree", "scope"}, fields["scope"]); failure != nil {
			return 0, failure
		}
	}
	end, failure := findBlockEnd(lines, start+2, "end tree", "text_tree_unterminated")
	if failure != nil {
		return 0, failure
	}
	if failure := validateTreeBody(lines[start+2:end-1], format, "text_tree"); failure != nil {
		return 0, failure
	}
	return end, nil
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
	fields, failure := decodeBlockHeader(lines[start], "inspect v2 ")
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
	bodyMode := scope
	if bodyMode == "tree" || bodyMode == "subtree" {
		// Both inspection scopes render complete detailed node definitions.
		bodyMode = "full"
	}
	if failure := validateTreeBody(lines[start+2:end-1], bodyMode, "text_inspect"); failure != nil {
		return 0, failure
	}
	return end, nil
}

// validateTreeBody keeps the probe from accepting a readable block whose body
// contradicts the complete-node state contract. Full definitions may omit only
// the known defaults; a delta set line may explicitly carry any tri-state value.
func validateTreeBody(body []string, mode, prefix string) *checkError {
	for _, rawLine := range body {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		tokens, ok := treeLineTokens(line)
		if !ok || len(tokens) == 0 {
			return failf(prefix + "_body_invalid")
		}
		if tokens[0] == "defaults" {
			return failf(prefix + "_body_invalid")
		}
		switch mode {
		case "full":
			if failure := validateFullNodeFlags(tokens, prefix); failure != nil {
				return failure
			}
		case "delta":
			switch tokens[0] {
			case "~":
				if failure := validateSetNodeFlags(tokens[1:], prefix); failure != nil {
					return failure
				}
			case "+", "=":
				if len(tokens) < 2 {
					return failf(prefix + "_body_invalid")
				}
				if failure := validateFullNodeFlags(tokens[1:], prefix); failure != nil {
					return failure
				}
			case "-", "children":
			default:
				return failf(prefix + "_body_invalid")
			}
		case "query":
			if strings.HasPrefix(tokens[0], "match") {
				continue
			}
			if failure := validateFullNodeFlags(tokens, prefix); failure != nil {
				return failure
			}
		default:
			return failf(prefix + "_body_invalid")
		}
	}
	return nil
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
		switch line[index] {
		case ' ', '\t':
			index++
			continue
		case '"':
			end, ok := treeJSONStringEnd(line, index)
			if !ok {
				return nil, false
			}
			tokens = append(tokens, line[index:end])
			index = end
		case '[':
			end, ok := treeBracketEnd(line, index)
			if !ok {
				return nil, false
			}
			tokens = append(tokens, line[index:end])
			index = end
		default:
			start := index
			for index < len(line) && line[index] != ' ' && line[index] != '\t' {
				index++
			}
			tokens = append(tokens, line[start:index])
		}
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
	pointer := "/" + strings.Join(escapePointerParts(path), "/")
	if seen[pointer] {
		return failf("text_result_path_duplicate")
	}
	_, failure := setNestedValue(object, path, value)
	if failure != nil {
		return failure
	}
	seen[pointer] = true
	return nil
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
			if _, numeric := parseArrayIndex(path[1]); numeric {
				child = make([]any, 0)
			} else {
				child = make(map[string]any)
			}
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
