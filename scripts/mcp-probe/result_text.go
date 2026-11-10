package main

import (
	"encoding/json/v2"
	"strconv"
	"strings"
)

// decodedTextResult is the probe-local oracle for result v1. It is deliberately
// narrow: it restores the facts this external probe checks, while keeping the
// decoded object available to prove that readable blocks preserve complete
// item/event JSON.
type decodedTextResult struct {
	view    resultView
	isError bool
	object  map[string]any
}

// decodeResultText parses one complete result v1 projection. It rejects a
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
	rawError, found := strings.CutPrefix(lines[0], "result v1 is_error=")
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
		"tree v1 ", "inspect v1 ", "shell v1 ", "apps v1 ", "timeline v1 ",
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
	case strings.HasPrefix(lines[start], "tree v1 "):
		return decodeTreeBlock(lines, start, object, seen)
	case strings.HasPrefix(lines[start], "inspect v1 "):
		return decodeInspectionBlock(lines, start, object, seen)
	case strings.HasPrefix(lines[start], "shell v1 "):
		return decodeShellBlock(lines, start, object, seen)
	case strings.HasPrefix(lines[start], "apps v1 "):
		return decodeAppsBlock(lines, start, object, seen)
	case strings.HasPrefix(lines[start], "timeline v1 "):
		return decodeTimelineBlock(lines, start, object, seen)
	default:
		return 0, failf("text_result_block_unknown")
	}
}

func decodeTreeBlock(lines []string, start int, object map[string]any,
	seen map[string]bool) (int, *checkError) {
	fields, failure := decodeBlockHeader(lines[start], "tree v1 ")
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
	if failure := setDecodedPath(object, seen, []string{"observation", "tree", "format"}, format); failure != nil {
		return 0, failure
	}
	if format == treeDelta {
		if failure := setDecodedPath(object, seen, []string{"observation", "tree", "base"}, fields["base"]); failure != nil {
			return 0, failure
		}
	}
	return findBlockEnd(lines, start, "end tree", "text_tree_unterminated")
}

func decodeInspectionBlock(lines []string, start int, object map[string]any,
	seen map[string]bool) (int, *checkError) {
	fields, failure := decodeBlockHeader(lines[start], "inspect v1 ")
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
	return findBlockEnd(lines, start, "end inspect", "text_inspect_unterminated")
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

func decodeAppsBlock(lines []string, start int, object map[string]any,
	seen map[string]bool) (int, *checkError) {
	fields, failure := decodeBlockHeader(lines[start], "apps v1 ")
	if failure != nil {
		return 0, failure
	}
	count, ok := parseInteger(fields["items"])
	if !ok || count < 0 {
		return 0, failf("text_apps_count_invalid")
	}
	total, ok := parseInteger(fields["total"])
	if !ok || total < 0 {
		return 0, failf("text_apps_total_invalid")
	}
	truncated, failure := decodeTextScalar(fields["truncated"])
	if failure != nil {
		return 0, failure
	}
	if _, ok := truncated.(bool); !ok {
		return 0, failf("text_apps_truncated_invalid")
	}
	items := make([]any, 0, count)
	end := start + 1
	for ; end < len(lines); end++ {
		line := lines[end]
		if line == "end apps" {
			if len(items) != count {
				return 0, failf("text_apps_count_mismatch")
			}
			for key, value := range map[string]any{
				"items":       items,
				"total":       total,
				"truncated":   truncated,
				"captured_at": fields["captured_at"],
			} {
				if key == "captured_at" && fields[key] == "" {
					continue
				}
				if failure := setDecodedPath(object, seen, []string{"apps", key}, value); failure != nil {
					return 0, failure
				}
			}
			return end + 1, nil
		}
		if !strings.HasPrefix(line, "item ") {
			return 0, failf("text_apps_item_invalid")
		}
		item, failure := decodeJSONValue(line[len("item "):])
		if failure != nil {
			return 0, failure
		}
		if _, ok := item.(map[string]any); !ok {
			return 0, failf("text_apps_item_not_object")
		}
		items = append(items, item)
	}
	return 0, failf("text_apps_unterminated")
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
	for index := start + 1; index < len(lines); index++ {
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
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
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
