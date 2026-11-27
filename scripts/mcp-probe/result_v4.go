package main

import (
	"encoding/json/v2"
	"strconv"
	"strings"
)

// This file is the probe-local, independent oracle for the result v4 compact
// envelope. It deliberately does not import the internal/mcp encoder: the probe
// compares the live text against its own reading of the documented grammar, so
// an encoder regression cannot be mirrored by a shared implementation.

// resultHeader is the parsed first line of one result v4 projection.
type resultHeader struct {
	Ok          bool
	OperationID string
	State       string
	Outcome     string
	HasOutcome  bool
	Phase       string
}

// v4Parser accumulates one decoded object. seen enforces the no-duplicate and
// no-ancestor/child-conflict rules across every line and block.
type v4Parser struct {
	object       map[string]any
	seen         map[string]bool
	header       resultHeader
	lastRank     int
	bindings     []treeBaseBinding
	haveObs      bool
	obsIDPresent bool
	obsScreenTok bool
	sessionSet   bool
	sessionNull  bool
	sessionLive  bool
}

// decodeResultText parses one complete result v4 projection. It rejects a
// missing/repeated envelope, legacy v3 headers, malformed blocks and duplicate
// paths.
func decodeResultText(text string) (*decodedTextResult, *checkError) {
	if strings.Contains(text, "\r") {
		return nil, failf("text_result_naked_cr")
	}
	if text == "" {
		return nil, failf("text_result_empty")
	}
	text = strings.TrimSuffix(text, "\n")
	if text == "" || strings.HasSuffix(text, "\n") {
		return nil, failf("text_result_trailing_blank")
	}
	lines := strings.Split(text, "\n")
	header, failure := parseResultHeader(lines[0])
	if failure != nil {
		return nil, failure
	}
	if len(lines) < 2 {
		return nil, failf("text_result_unterminated")
	}
	if lines[len(lines)-1] != "end result" {
		return nil, failf("text_result_unterminated")
	}

	parser := &v4Parser{
		object: make(map[string]any),
		seen:   make(map[string]bool),
		header: *header,
	}
	if header.OperationID != "" {
		if failure := parser.set([]string{"operation_id"}, header.OperationID); failure != nil {
			return nil, failure
		}
	}
	if header.State != "" {
		if failure := parser.set([]string{"operation_state"}, header.State); failure != nil {
			return nil, failure
		}
	}
	if header.HasOutcome {
		if failure := parser.set([]string{"outcome"}, header.Outcome); failure != nil {
			return nil, failure
		}
	}
	if header.Phase != "" {
		if failure := parser.set([]string{"phase"}, header.Phase); failure != nil {
			return nil, failure
		}
	}
	for index := 1; index < len(lines)-1; {
		line := lines[index]
		if strings.Contains(line, " omitted ") &&
			(strings.HasPrefix(line, "tree v") || strings.HasPrefix(line, "inspect v") ||
				strings.HasPrefix(line, "shell v") || strings.HasPrefix(line, "timeline v") ||
				strings.HasPrefix(line, "app_items v") || strings.HasPrefix(line, "app_tasks v")) {
			return nil, failf("text_result_block_omitted")
		}
		if isForeignV4BlockHeader(line) {
			return nil, failf("text_result_block_unknown")
		}
		if line == "" {
			if index+1 >= len(lines)-1 {
				return nil, failf("text_result_blank_invalid")
			}
			if !isV4BlockHeader(lines[index+1]) {
				if isForeignV4BlockHeader(lines[index+1]) {
					return nil, failf("text_result_block_unknown")
				}
				return nil, failf("text_result_blank_invalid")
			}
			index++
			continue
		}
		if isV4BlockHeader(line) {
			if index == 1 || lines[index-1] != "" {
				return nil, failf("text_result_block_separator_missing")
			}
			next, failure := parser.decodeBlock(lines, index)
			if failure != nil {
				return nil, failure
			}
			index = next
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			return nil, failf("text_result_line_invalid")
		}
		next, failure := parser.decodeLine(lines, index)
		if failure != nil {
			return nil, failure
		}
		index = next
	}
	if failure := parser.finish(); failure != nil {
		return nil, failure
	}

	encoded, err := json.Marshal(parser.object)
	if err != nil {
		return nil, failf("text_result_unusable")
	}
	var view resultView
	if err := json.Unmarshal(encoded, &view); err != nil {
		return nil, failf("text_result_unusable")
	}
	return &decodedTextResult{
		view:    view,
		isError: !header.Ok,
		object:  parser.object,
		header:  *header,
	}, nil
}

// parseResultHeader reads the fixed `result v4 <ok|err>` header plus the ordered
// optional scalars op/state/outcome/phase.
func parseResultHeader(line string) (*resultHeader, *checkError) {
	if !strings.HasPrefix(line, "result v4 ") {
		return nil, failf("text_result_header_invalid")
	}
	rest := strings.TrimPrefix(line, "result v4 ")
	if rest == "" || strings.HasPrefix(rest, " ") || strings.ContainsAny(rest, "\t") ||
		strings.Contains(rest, "  ") {
		return nil, failf("text_result_header_invalid")
	}
	tokens := strings.Fields(rest)
	header := &resultHeader{}
	switch tokens[0] {
	case "ok":
		header.Ok = true
	case "err":
	default:
		return nil, failf("text_result_header_invalid")
	}
	order := map[string]int{"op": 0, "state": 1, "outcome": 2, "phase": 3}
	last := -1
	for _, token := range tokens[1:] {
		key, value, found := strings.Cut(token, "=")
		rank, known := order[key]
		if !found || !known || value == "" || rank <= last {
			return nil, failf("text_result_header_field_invalid")
		}
		if !resultToken(value) {
			return nil, failf("text_result_header_field_invalid", field("field", key))
		}
		last = rank
		switch key {
		case "op":
			if !validReference(value, 'P') {
				return nil, failf("text_result_header_field_invalid", field("field", key))
			}
			header.OperationID = value
		case "state":
			header.State = value
		case "outcome":
			header.HasOutcome = true
			header.Outcome = value
		case "phase":
			header.Phase = value
		}
	}
	return header, nil
}

// resultToken is the shared unquoted-token rule: non-empty and free of
// whitespace, '=', '"', '\\' and control characters.
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

func (p *v4Parser) set(path []string, value any) *checkError {
	return setDecodedPath(p.object, p.seen, path, value)
}

// enter enforces the fixed top-level section order.
func (p *v4Parser) enter(rank int) *checkError {
	if rank < p.lastRank {
		return failf("text_result_order_invalid")
	}
	p.lastRank = rank
	return nil
}

func v4Tokens(line, prefix string) ([]string, *checkError) {
	if !strings.HasPrefix(line, prefix) {
		return nil, failf("text_result_line_invalid")
	}
	rest := strings.TrimPrefix(line, prefix)
	if strings.ContainsRune(rest, '\t') {
		return nil, failf("text_result_line_invalid")
	}
	if strings.HasSuffix(prefix, " ") {
		if rest == "" || strings.HasPrefix(rest, " ") || strings.HasSuffix(rest, " ") {
			return nil, failf("text_result_line_invalid")
		}
	} else {
		if rest == "" {
			return nil, nil
		}
		if !strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "  ") ||
			strings.HasSuffix(rest, " ") {
			return nil, failf("text_result_line_invalid")
		}
		rest = rest[1:]
		if rest == "" {
			return nil, failf("text_result_line_invalid")
		}
	}
	return v4Split(rest)
}

// v4Split splits one token sequence while keeping JSON strings and bracketed
// arrays whole. Separators are exactly one space; a double or trailing space is
// an invalid separator, not part of a quoted payload.
func v4Split(rest string) ([]string, *checkError) {
	var tokens []string
	for index := 0; index < len(rest); {
		start := index
		for index < len(rest) && rest[index] != ' ' {
			switch rest[index] {
			case '"':
				end, ok := treeJSONStringEnd(rest, index)
				if !ok {
					return nil, failf("text_result_line_invalid")
				}
				index = end
			case '[':
				end, ok := treeBracketEnd(rest, index)
				if !ok {
					return nil, failf("text_result_line_invalid")
				}
				index = end
			default:
				index++
			}
		}
		tokens = append(tokens, rest[start:index])
		if index < len(rest) {
			if index+1 < len(rest) && rest[index+1] == ' ' {
				return nil, failf("text_result_line_invalid")
			}
			index++
		}
	}
	return tokens, nil
}

func isV4BlockHeader(line string) bool {
	for _, prefix := range []string{
		"tree v5 ", "inspect v3 ", "shell v1 ", "timeline v1 ",
		"app_items v1 ", "app_tasks v1 ",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// isForeignV4BlockHeader rejects every block version outside the accepted set
// so an old block cannot be consumed line by line as ordinary scalars.
func isForeignV4BlockHeader(line string) bool {
	for _, family := range []string{
		"tree v", "inspect v", "shell v", "timeline v", "app_items v", "app_tasks v",
	} {
		if strings.HasPrefix(line, family) {
			return !isV4BlockHeader(line)
		}
	}
	return false
}

// decodeBlock dispatches one blank-line-separated readable block.
func (p *v4Parser) decodeBlock(lines []string, start int) (int, *checkError) {
	line := lines[start]
	switch {
	case strings.HasPrefix(line, "tree v5 "):
		if failure := p.enter(15); failure != nil {
			return 0, failure
		}
		return decodeTreeBlock(lines, start, p.object, p.seen, &p.bindings)
	case strings.HasPrefix(line, "inspect v3 "):
		if failure := p.enter(16); failure != nil {
			return 0, failure
		}
		return decodeInspectionBlock(lines, start, p.object, p.seen)
	case strings.HasPrefix(line, "app_items v1 "):
		if failure := p.enter(18); failure != nil {
			return 0, failure
		}
		return p.decodeAppItems(lines, start)
	case strings.HasPrefix(line, "app_tasks v1 "):
		if failure := p.enter(18); failure != nil {
			return 0, failure
		}
		return p.decodeAppTasks(lines, start)
	case strings.HasPrefix(line, "shell v1 "):
		if failure := p.enter(19); failure != nil {
			return 0, failure
		}
		return decodeShellBlock(lines, start, p.object, p.seen)
	case strings.HasPrefix(line, "timeline v1 "):
		if failure := p.enter(22); failure != nil {
			return 0, failure
		}
		return decodeTimelineBlock(lines, start, p.object, p.seen)
	default:
		return 0, failf("text_result_block_unknown")
	}
}

// decodeLine dispatches one ordinary result v4 body line.
func (p *v4Parser) decodeLine(lines []string, index int) (int, *checkError) {
	line := lines[index]
	switch {
	case strings.HasPrefix(line, "effect="):
		return 0, failf("text_result_effect_present")
	case strings.HasPrefix(line, "text_input "):
		if failure := p.enter(2); failure != nil {
			return 0, failure
		}
		if failure := p.decodeTextInput(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "target_reference="):
		if failure := p.enter(3); failure != nil {
			return 0, failure
		}
		if failure := p.setSimpleToken(line, "target_reference", "target_reference"); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "request_closed="):
		if failure := p.enter(4); failure != nil {
			return 0, failure
		}
		if failure := p.setBool(line, "request_closed", "request_closed"); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "blocking_operation_id="):
		if failure := p.enter(5); failure != nil {
			return 0, failure
		}
		if failure := p.setSimpleToken(line, "blocking_operation_id", "blocking_operation_id"); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "can_start="):
		if failure := p.enter(6); failure != nil {
			return 0, failure
		}
		if failure := p.setBool(line, "can_start", "can_start"); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "blocking_code="):
		if failure := p.enter(7); failure != nil {
			return 0, failure
		}
		if failure := p.setSimpleToken(line, "blocking_code", "blocking_code"); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "start_operation_id="):
		if failure := p.enter(8); failure != nil {
			return 0, failure
		}
		if failure := p.setSimpleToken(line, "start_operation_id", "start_operation_id"); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "observation_state="):
		if failure := p.enter(9); failure != nil {
			return 0, failure
		}
		if failure := p.setSimpleToken(line, "observation_state", "observation_state"); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "input_state="):
		if failure := p.enter(10); failure != nil {
			return 0, failure
		}
		if failure := p.setSimpleToken(line, "input_state", "input_state"); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "notify.error.details="):
		if failure := p.enter(11); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorDetails(line, "notify.error.details=",
			[]string{"notification", "error", "details"}, true); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "notify.error "):
		if failure := p.enter(11); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorLine(line, "notify.error ", []string{"notification", "error"}); failure != nil {
			return 0, failure
		}
	case line == "notify" || strings.HasPrefix(line, "notify "):
		if failure := p.enter(11); failure != nil {
			return 0, failure
		}
		if failure := p.decodeNotify(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "image.error.details="):
		if failure := p.enter(12); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorDetails(line, "image.error.details=",
			[]string{"image", "error", "details"}, false); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "image.error "):
		if failure := p.enter(12); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorLine(line, "image.error ", []string{"image", "error"}); failure != nil {
			return 0, failure
		}
	case line == "image" || strings.HasPrefix(line, "image "):
		if failure := p.enter(12); failure != nil {
			return 0, failure
		}
		if failure := p.decodeImage(line, "image", []string{"image"}); failure != nil {
			return 0, failure
		}
	case line == "session=null":
		if failure := p.enter(13); failure != nil {
			return 0, failure
		}
		if failure := p.set([]string{"session"}, nil); failure != nil {
			return 0, failure
		}
		p.sessionSet = true
		p.sessionNull = true
	case strings.HasPrefix(line, "session "):
		if failure := p.enter(13); failure != nil {
			return 0, failure
		}
		if failure := p.decodeSession(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "obs.image.error.details="):
		if failure := p.enter(14); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorDetails(line, "obs.image.error.details=",
			[]string{"observation", "image", "error", "details"}, false); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "obs.image.error "):
		if failure := p.enter(14); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorLine(line, "obs.image.error ",
			[]string{"observation", "image", "error"}); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "obs.image "):
		if failure := p.enter(14); failure != nil {
			return 0, failure
		}
		if failure := p.decodeImage(line, "obs.image ", []string{"observation", "image"}); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "obs.error.details="):
		if failure := p.enter(14); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorDetails(line, "obs.error.details=",
			[]string{"observation", "error", "details"}, false); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "obs.error "):
		if failure := p.enter(14); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorLine(line, "obs.error ", []string{"observation", "error"}); failure != nil {
			return 0, failure
		}
	case line == "obs" || strings.HasPrefix(line, "obs "):
		if failure := p.enter(14); failure != nil {
			return 0, failure
		}
		if failure := p.decodeObs(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "app "):
		if failure := p.enter(17); failure != nil {
			return 0, failure
		}
		if failure := p.decodeApp(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "wait "):
		if failure := p.enter(20); failure != nil {
			return 0, failure
		}
		if failure := p.decodeWait(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "diagnosis.issues="):
		if failure := p.enter(21); failure != nil {
			return 0, failure
		}
		if failure := p.decodeJSONField(line, "diagnosis.issues=", []string{"diagnosis", "issues"}); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "diagnosis.error_counts="):
		if failure := p.enter(21); failure != nil {
			return 0, failure
		}
		if failure := p.decodeDiagnosisErrorCounts(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "diagnosis.timing_ms="):
		if failure := p.enter(21); failure != nil {
			return 0, failure
		}
		if failure := p.decodeJSONField(line, "diagnosis.timing_ms=", []string{"diagnosis", "timing_ms"}); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "diagnosis "):
		if failure := p.enter(21); failure != nil {
			return 0, failure
		}
		if failure := p.decodeDiagnosis(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "cleanup.error.details="):
		if failure := p.enter(23); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorDetails(line, "cleanup.error.details=",
			[]string{"cleanup", "error", "details"}, false); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "cleanup.error "):
		if failure := p.enter(23); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorLine(line, "cleanup.error ", []string{"cleanup", "error"}); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "cleanup "):
		if failure := p.enter(23); failure != nil {
			return 0, failure
		}
		if failure := p.decodeCleanup(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "error.details="):
		if failure := p.enter(24); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorDetails(line, "error.details=",
			[]string{"error", "details"}, false); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "error "):
		if failure := p.enter(24); failure != nil {
			return 0, failure
		}
		if failure := p.decodeErrorLine(line, "error ", []string{"error"}); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "recovery "):
		if failure := p.enter(25); failure != nil {
			return 0, failure
		}
		if failure := p.decodeRecovery(line); failure != nil {
			return 0, failure
		}
	case strings.HasPrefix(line, "json v1 "):
		path, value, failure := decodeJSONFallback(line)
		if failure != nil {
			return 0, failure
		}
		if isTypedAppPath(path) {
			return 0, failf("text_result_app_field_typed_required")
		}
		if failure := setJSONPointer(p.object, p.seen, path, value); failure != nil {
			return 0, failure
		}
	default:
		return 0, failf("text_result_item_invalid")
	}
	return index + 1, nil
}

func (p *v4Parser) setSimpleToken(line, key, fieldName string) *checkError {
	value := strings.TrimPrefix(line, key+"=")
	if !resultToken(value) {
		return failf("text_result_value_invalid", field("field", fieldName))
	}
	return p.set([]string{key}, value)
}

func (p *v4Parser) setBool(line, key, fieldName string) *checkError {
	value := strings.TrimPrefix(line, key+"=")
	if value != "true" && value != "false" {
		return failf("text_result_value_invalid", field("field", fieldName))
	}
	return p.set([]string{key}, value == "true")
}

func (p *v4Parser) decodeTextInput(line string) *checkError {
	tokens, failure := v4Tokens(line, "text_input ")
	if failure != nil {
		return failure
	}
	if len(tokens) != 2 {
		return failf("text_result_text_input_invalid")
	}
	values := make(map[string]string, 2)
	for index, token := range tokens {
		key, value, found := strings.Cut(token, "=")
		if !found || !resultToken(value) {
			return failf("text_result_text_input_invalid")
		}
		if (index == 0 && key != "write") || (index == 1 && key != "submit") {
			return failf("text_result_text_input_invalid")
		}
		values[key] = value
	}
	if values["write"] == "" || values["submit"] == "" {
		return failf("text_result_text_input_invalid")
	}
	for _, member := range []string{"write", "submit"} {
		if failure := p.set([]string{"text_input", member}, values[member]); failure != nil {
			return failure
		}
	}
	return nil
}

func (p *v4Parser) decodeJSONField(line, prefix string, path []string) *checkError {
	value, failure := decodeJSONValue(strings.TrimPrefix(line, prefix))
	if failure != nil {
		return failure
	}
	return p.set(path, value)
}

// decodeDiagnosisErrorCounts parses the bounded controlled-code count map. The
// documented result v4 line carries a non-empty JSON object whose keys are
// non-empty control codes and whose values are positive integers; null, empty
// objects, empty keys and non-positive or non-integer counts are rejected.
func (p *v4Parser) decodeDiagnosisErrorCounts(line string) *checkError {
	value, failure := decodeJSONValue(strings.TrimPrefix(line, "diagnosis.error_counts="))
	if failure != nil {
		return failure
	}
	counts, ok := value.(map[string]any)
	if !ok || len(counts) == 0 {
		return failf("text_result_diagnosis_error_counts_invalid")
	}
	for code, raw := range counts {
		count, valid := valueInteger(raw)
		if code == "" || !valid || count <= 0 {
			return failf("text_result_diagnosis_error_counts_invalid")
		}
	}
	return p.set([]string{"diagnosis", "error_counts"}, value)
}

// decodeErrorLine parses `<prefix><code> "<message>"` and stores an error
// object. The message uses standard JSON string encoding, including "".
func (p *v4Parser) decodeErrorLine(line, prefix string, path []string) *checkError {
	tokens, ok := treeLineTokens(strings.TrimPrefix(line, prefix))
	if !ok || len(tokens) != 2 || !resultToken(tokens[0]) || !isTreeJSONString(tokens[1]) {
		return failf("text_result_error_invalid")
	}
	message, failure := decodeJSONValue(tokens[1])
	if failure != nil {
		return failure
	}
	if failure := p.set(append(path, "code"), tokens[0]); failure != nil {
		return failure
	}
	return p.set(append(path, "message"), message)
}

func (p *v4Parser) decodeErrorDetails(line, prefix string, path []string, allowLoose bool) *checkError {
	value, failure := decodeJSONValue(strings.TrimPrefix(line, prefix))
	if failure != nil {
		return failure
	}
	if !allowLoose {
		if _, ok := value.(map[string]any); !ok {
			return failf("text_result_error_details_invalid")
		}
	}
	return p.set(path, value)
}

func (p *v4Parser) decodeNotify(line string) *checkError {
	tokens, failure := v4Tokens(line, "notify")
	if failure != nil {
		return failure
	}
	if len(tokens) == 0 {
		return failf("text_result_notify_empty")
	}
	order := map[string]int{"id": 0, "saved": 1, "state": 2}
	last := -1
	saw := false
	for _, token := range tokens {
		key, value, found := strings.Cut(token, "=")
		rank, known := order[key]
		if !found || !known || rank <= last {
			return failf("text_result_notify_invalid")
		}
		last = rank
		saw = true
		switch key {
		case "id":
			if !resultToken(value) {
				return failf("text_result_notify_invalid")
			}
			if failure := p.set([]string{"message_id"}, value); failure != nil {
				return failure
			}
		case "saved":
			if value != "true" && value != "false" {
				return failf("text_result_notify_invalid")
			}
			if failure := p.set([]string{"saved"}, value == "true"); failure != nil {
				return failure
			}
		case "state":
			if !resultToken(value) {
				return failf("text_result_notify_invalid")
			}
			if failure := p.set([]string{"notification", "state"}, value); failure != nil {
				return failure
			}
		}
	}
	if !saw {
		return failf("text_result_notify_empty")
	}
	return nil
}

func (p *v4Parser) decodeImage(line, prefix string, path []string) *checkError {
	tokens, failure := v4Tokens(line, prefix)
	if failure != nil {
		return failure
	}
	if len(tokens) == 0 {
		return failf("text_result_image_empty")
	}
	last := -1
	var width, height int
	haveGeometry := false
	var rotation int
	haveRotation := false
	var screen map[string]any
	var screenWidth, screenHeight int
	var mediaType string
	readOnly := false
	capturedAt := ""
	for _, token := range tokens {
		rank, ok := imageTokenRank(token)
		if !ok || rank <= last {
			return failf("text_result_image_invalid")
		}
		last = rank
		switch {
		case token == "read_only":
			readOnly = true
		case strings.HasPrefix(token, "media="):
			mediaType = strings.TrimPrefix(token, "media=")
			if !resultToken(mediaType) {
				return failf("text_result_image_invalid")
			}
			if mediaType == imageJPEG {
				return failf("text_result_image_media_redundant")
			}
		case strings.HasPrefix(token, "screen="):
			var display *int
			screenWidth, screenHeight, display, failure = parseGeometryToken(strings.TrimPrefix(token, "screen="))
			if failure != nil {
				return failure
			}
			screen = map[string]any{"width": screenWidth, "height": screenHeight, "rotation": 0}
			if display != nil {
				screen["display_id"] = *display
			}
		case strings.HasPrefix(token, "rot="):
			rotation, failure = parseRotation(strings.TrimPrefix(token, "rot="))
			if failure != nil {
				return failure
			}
			haveRotation = true
		case strings.HasPrefix(token, "at="):
			capturedAt = strings.TrimPrefix(token, "at=")
			if !validTimeToken(capturedAt) {
				return failf("text_result_image_invalid", field("field", "at"))
			}
		default:
			var display *int
			width, height, display, failure = parseGeometryToken(token)
			if failure != nil {
				return failf("text_result_image_invalid")
			}
			haveGeometry = true
			_ = display
		}
	}
	if haveGeometry {
		if failure := p.set(append(path, "width"), width); failure != nil {
			return failure
		}
		if failure := p.set(append(path, "height"), height); failure != nil {
			return failure
		}
	}
	if mediaType == "" {
		mediaType = imageJPEG
	}
	if failure := p.set(append(path, "media_type"), mediaType); failure != nil {
		return failure
	}
	if readOnly {
		if failure := p.set(append(path, "read_only"), true); failure != nil {
			return failure
		}
	}
	if capturedAt != "" {
		if failure := p.set(append(path, "captured_at"), capturedAt); failure != nil {
			return failure
		}
	}
	if screen != nil {
		if haveRotation {
			screen["rotation"] = rotation
		}
		if failure := p.set(append(path, "screen"), screen); failure != nil {
			return failure
		}
	} else if haveRotation {
		if failure := p.set(append(path, "screen"), map[string]any{"rotation": rotation}); failure != nil {
			return failure
		}
	}
	return nil
}

func imageTokenRank(token string) (int, bool) {
	switch {
	case token == "read_only":
		return 1, true
	case strings.HasPrefix(token, "media="):
		return 2, true
	case strings.HasPrefix(token, "screen="):
		return 3, true
	case strings.HasPrefix(token, "rot="):
		return 4, true
	case strings.HasPrefix(token, "at="):
		return 5, true
	case looksLikeGeometryToken(token):
		return 0, true
	default:
		return 0, false
	}
}

func looksLikeGeometryToken(token string) bool {
	parts := strings.SplitN(token, "x", 2)
	if len(parts) != 2 {
		return false
	}
	_, firstOK := parseNonNegativeDecimal(parts[0])
	second := parts[1]
	if at := strings.IndexByte(second, '@'); at >= 0 {
		second = second[:at]
	}
	_, secondOK := parseNonNegativeDecimal(second)
	return firstOK && secondOK
}

// parseGeometryToken reads `<W>x<H>[@<display_id>]`. A nil display id keeps the
// distinction from display_id=0.
func parseGeometryToken(token string) (int, int, *int, *checkError) {
	parts := strings.SplitN(token, "x", 2)
	if len(parts) != 2 {
		return 0, 0, nil, failf("text_result_geometry_invalid")
	}
	width, ok := parsePositiveDecimal(parts[0])
	if !ok {
		return 0, 0, nil, failf("text_result_geometry_invalid")
	}
	second := parts[1]
	var display *int
	if at := strings.IndexByte(second, '@'); at >= 0 {
		value, ok := parseNonNegativeDecimal(second[at+1:])
		if !ok {
			return 0, 0, nil, failf("text_result_geometry_invalid")
		}
		display = &value
		second = second[:at]
	}
	height, ok := parsePositiveDecimal(second)
	if !ok {
		return 0, 0, nil, failf("text_result_geometry_invalid")
	}
	return width, height, display, nil
}

func parseRotation(value string) (int, *checkError) {
	rotation, ok := parsePositiveDecimal(value)
	if !ok || rotation > 3 {
		return 0, failf("text_result_rotation_invalid")
	}
	return rotation, nil
}

func validClockTime(value string) bool {
	if len(value) != 8 || value[2] != ':' || value[5] != ':' {
		return false
	}
	for _, index := range []int{0, 1, 3, 4, 6, 7} {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	hour, _ := strconv.Atoi(value[0:2])
	minute, _ := strconv.Atoi(value[3:5])
	second, _ := strconv.Atoi(value[6:8])
	return hour < 24 && minute < 60 && second < 60
}

// validTimeToken accepts the truncated HH:MM:SS form and any safe literal the
// encoder kept because it was not a parseable RFC3339 value.
func validTimeToken(value string) bool {
	return validClockTime(value) || resultToken(value)
}

func (p *v4Parser) decodeSession(line string) *checkError {
	tokens, failure := v4Tokens(line, "session ")
	if failure != nil {
		return failure
	}
	if len(tokens) < 3 {
		return failf("text_result_session_invalid")
	}
	state := tokens[0]
	if !resultToken(state) {
		return failf("text_result_session_invalid")
	}
	if failure := p.set([]string{"session", "state"}, state); failure != nil {
		return failure
	}
	order := map[string]int{"mode": 0, "origin": 1, "started": 2, "expires": 3, "ended": 4, "reason": 5}
	last := -1
	seenMode, seenOrigin := false, false
	for _, token := range tokens[1:] {
		key, value, found := strings.Cut(token, "=")
		rank, known := order[key]
		if !found || !known || rank <= last || !resultToken(value) {
			return failf("text_result_session_invalid")
		}
		last = rank
		switch key {
		case "mode":
			seenMode = true
			if failure := p.set([]string{"session", "mode"}, value); failure != nil {
				return failure
			}
		case "origin":
			seenOrigin = true
			if !validReference(value, 'P') {
				return failf("text_result_session_invalid", field("field", "origin"))
			}
			if failure := p.set([]string{"session", "origin"}, value); failure != nil {
				return failure
			}
		case "started":
			if !validTimeToken(value) {
				return failf("text_result_session_invalid", field("field", "started"))
			}
			if failure := p.set([]string{"session", "started_at"}, value); failure != nil {
				return failure
			}
		case "expires":
			if !validTimeToken(value) {
				return failf("text_result_session_invalid", field("field", "expires"))
			}
			if failure := p.set([]string{"session", "expires_at"}, value); failure != nil {
				return failure
			}
		case "ended":
			if !validTimeToken(value) {
				return failf("text_result_session_invalid", field("field", "ended"))
			}
			if failure := p.set([]string{"session", "ended_at"}, value); failure != nil {
				return failure
			}
		case "reason":
			if failure := p.set([]string{"session", "reason"}, value); failure != nil {
				return failure
			}
		}
	}
	if !seenMode || !seenOrigin {
		return failf("text_result_session_invalid")
	}
	p.sessionSet = true
	p.sessionNull = false
	p.sessionLive = true
	return nil
}

func (p *v4Parser) decodeObs(line string) *checkError {
	tokens, failure := v4Tokens(line, "obs")
	if failure != nil {
		return failure
	}
	if len(tokens) == 0 {
		return failf("text_result_obs_empty")
	}
	index := 0
	id := ""
	if !strings.Contains(tokens[0], "=") {
		if !validTreeTextO(tokens[0]) {
			return failf("text_result_obs_invalid", field("field", "id"))
		}
		id = tokens[0]
		index++
	}
	order := map[string]int{"status": 0, "fg": 1, "at": 2, "visual": 3, "change": 4, "hint": 5, "quality": 6, "screen": 7, "rot": 8, "missing": 9}
	last := -1
	var statusToken string
	var rotation int
	haveRotation := false
	statusExplicit := false
	fields := 0
	for ; index < len(tokens); index++ {
		token := tokens[index]
		key, value, found := strings.Cut(token, "=")
		rank, known := order[key]
		if !found || !known || rank <= last || value == "" {
			return failf("text_result_obs_invalid")
		}
		last = rank
		fields++
		switch key {
		case "status":
			if !resultToken(value) {
				return failf("text_result_obs_invalid", field("field", "status"))
			}
			statusToken = value
			statusExplicit = true
		case "fg":
			if failure := p.set([]string{"observation", "foreground"},
				parseForegroundToken(value)); failure != nil {
				return failure
			}
		case "at":
			if !validTimeToken(value) {
				return failf("text_result_obs_invalid", field("field", "at"))
			}
			if failure := p.set([]string{"observation", "captured_at"}, value); failure != nil {
				return failure
			}
		case "visual":
			if !validVisualHintToken(value) {
				return failf("text_result_obs_invalid", field("field", "visual"))
			}
			if failure := p.set([]string{"observation", "visual"}, value); failure != nil {
				return failure
			}
		case "change":
			if !validTreeChangeToken(value) {
				return failf("text_result_obs_invalid", field("field", "change"))
			}
			if failure := p.set([]string{"observation", "tree_change"}, value); failure != nil {
				return failure
			}
		case "hint":
			if !validObservationHintToken(value) {
				return failf("text_result_obs_invalid", field("field", "hint"))
			}
			if failure := p.set([]string{"observation", "hint"}, value); failure != nil {
				return failure
			}
		case "quality":
			return failf("text_result_obs_quality_present")
		case "screen":
			width, height, display, parseFailure := parseGeometryToken(value)
			if parseFailure != nil {
				return parseFailure
			}
			p.obsScreenTok = true
			screen := map[string]any{"width": width, "height": height, "rotation": 0}
			if display != nil {
				screen["display_id"] = *display
			}
			if failure := p.set([]string{"observation", "screen"}, screen); failure != nil {
				return failure
			}
		case "rot":
			rotation, failure = parseRotation(value)
			if failure != nil {
				return failure
			}
			haveRotation = true
		case "missing":
			values := make([]any, 0)
			for _, item := range strings.Split(value, ",") {
				if !resultToken(item) {
					return failf("text_result_obs_invalid", field("field", "missing"))
				}
				values = append(values, item)
			}
			if failure := p.set([]string{"observation", "missing"}, values); failure != nil {
				return failure
			}
		}
	}
	if id == "" && fields == 0 {
		return failf("text_result_obs_empty")
	}
	if id != "" {
		if failure := p.set([]string{"observation", "id"}, id); failure != nil {
			return failure
		}
		p.obsIDPresent = true
	}
	if statusToken == "" {
		statusToken = observationComplete
	}
	if failure := p.set([]string{"observation", "status"}, statusToken); failure != nil {
		return failure
	}
	if statusExplicit && statusToken == observationComplete && (id != "" || fields > 1) {
		return failf("text_result_obs_status_redundant")
	}
	if haveRotation {
		existing, found := lookupPath(p.object, []string{"observation", "screen"})
		if !found {
			if failure := p.set([]string{"observation", "screen"},
				map[string]any{"rotation": rotation}); failure != nil {
				return failure
			}
		} else if screen, ok := existing.(map[string]any); ok {
			screen["rotation"] = rotation
		}
	}
	p.haveObs = true
	return nil
}

// validVisualHintToken checks the published visual grammar independently of
// the server, including the canonical image percentage range of 45..100.
func validVisualHintToken(value string) bool {
	switch value {
	case "loading", "media_surface", "sparse_tree":
		return true
	}
	rest, found := strings.CutPrefix(value, "image_heavy(")
	if !found || !strings.HasSuffix(rest, "%)") {
		return false
	}
	digits := strings.TrimSuffix(rest, "%)")
	if digits == "" || digits[0] < '1' || digits[0] > '9' {
		return false
	}
	for index := 1; index < len(digits); index++ {
		if digits[index] < '0' || digits[index] > '9' {
			return false
		}
	}
	percent, err := strconv.Atoi(digits)
	return err == nil && percent >= 45 && percent <= 100
}

func validTreeChangeToken(value string) bool {
	return value == "unchanged" || value == "detail_only" || value == "matched_base"
}

func validObservationHintToken(value string) bool {
	return value == "snap" || value == "wait_for"
}

func parseForegroundToken(token string) any {
	switch token {
	case "none":
		return map[string]any{"state": "none"}
	case "present":
		return map[string]any{"state": "present"}
	case "unknown":
		return map[string]any{"state": "unknown"}
	}
	if strings.HasPrefix(token, "unknown(") && strings.HasSuffix(token, ")") {
		reason := token[len("unknown(") : len(token)-1]
		if strings.HasPrefix(reason, `"`) {
			if decoded, failure := decodeJSONValue(reason); failure == nil {
				if text, ok := decoded.(string); ok {
					return map[string]any{"state": "unknown", "reason": text}
				}
			}
		}
		if resultToken(reason) {
			return map[string]any{"state": "unknown", "reason": reason}
		}
		return map[string]any{"state": "unknown"}
	}
	if strings.HasPrefix(token, `"`) {
		if decoded, failure := decodeJSONValue(token); failure == nil {
			if text, ok := decoded.(string); ok {
				return map[string]any{"state": "present", "package": text}
			}
		}
		return map[string]any{"state": "present"}
	}
	if strings.HasPrefix(token, "#") {
		if taskID, ok := parsePositiveDecimal(strings.TrimPrefix(token, "#")); ok {
			return map[string]any{"state": "present", "task_id": taskID}
		}
		return map[string]any{"state": "present"}
	}
	if hash := strings.IndexByte(token, '#'); hash > 0 {
		pkg := token[:hash]
		if taskID, ok := parsePositiveDecimal(token[hash+1:]); ok && resultToken(pkg) {
			return map[string]any{"state": "present", "package": pkg, "task_id": taskID}
		}
	}
	if resultToken(token) {
		return map[string]any{"state": "present", "package": token}
	}
	return map[string]any{"state": "present"}
}

func (p *v4Parser) decodeApp(line string) *checkError {
	tokens, failure := v4Tokens(line, "app ")
	if failure != nil {
		return failure
	}
	if len(tokens) == 0 {
		return failf("text_result_app_invalid")
	}
	kind := tokens[0]
	if !appKindSet[kind] {
		return failf("text_result_app_kind_invalid", field("kind", kind))
	}
	if failure := p.set([]string{"app", "kind"}, kind); failure != nil {
		return failure
	}
	order := map[string]int{
		"pkg": 0, "task": 1, "user": 2, "src_task": 3, "target_tasks": 4,
		"src_display": 5, "display": 6, "remaining_tasks": 7,
		"ownership": 8, "reused": 9, "state_preserved": 10, "fg_restore": 11,
		"complete": 12, "target_pids": 13, "killed_pids": 14, "remaining_pids": 15,
		"restarted": 16, "package_state": 17, "total": 18, "truncated": 19, "fg": 20,
		"at": 21,
	}
	last := -1
	for _, token := range tokens[1:] {
		key, value, found := strings.Cut(token, "=")
		rank, known := order[key]
		if !found || !known || rank <= last || value == "" {
			return failf("text_result_app_invalid")
		}
		last = rank
		switch key {
		case "pkg":
			text, failure := decodePackageToken(value)
			if failure != nil {
				return failure
			}
			if failure := p.set([]string{"app", "package"}, text); failure != nil {
				return failure
			}
		case "task", "src_task", "src_display", "display":
			number, ok := parseNonNegativeDecimal(value)
			if !ok || ((key == "task" || key == "src_task") && number == 0) {
				return failf("text_result_app_invalid", field("field", key))
			}
			target := map[string]string{
				"task": "task_id", "src_task": "source_task_id",
				"src_display": "source_display_id", "display": "display_id",
			}[key]
			if failure := p.set([]string{"app", target}, number); failure != nil {
				return failure
			}
		case "user":
			number, ok := parseNonNegativeDecimal(value)
			if !ok {
				return failf("text_result_app_invalid", field("field", key))
			}
			if failure := p.set([]string{"app", "android_user_id"}, number); failure != nil {
				return failure
			}
		case "target_tasks", "remaining_tasks":
			values, failure := parseSortedTaskIDArray(value, key)
			if failure != nil {
				return failure
			}
			target := map[string]string{
				"target_tasks": "target_task_ids", "remaining_tasks": "remaining_task_ids",
			}[key]
			if failure := p.set([]string{"app", target}, values); failure != nil {
				return failure
			}
		case "package_state":
			if !appPackageStateSet[value] {
				return failf("text_result_app_invalid", field("field", key))
			}
			if failure := p.set([]string{"app", "package_state"}, value); failure != nil {
				return failure
			}
		case "ownership":
			if !appOwnershipSet[value] {
				return failf("text_result_app_invalid", field("field", key))
			}
			if failure := p.set([]string{"app", "ownership"}, value); failure != nil {
				return failure
			}
		case "reused", "complete", "truncated":
			if value != "true" && value != "false" {
				return failf("text_result_app_invalid", field("field", key))
			}
			if failure := p.set([]string{"app", key}, value == "true"); failure != nil {
				return failure
			}
		case "state_preserved", "restarted":
			parsed, ok := parseNullableBoolToken(value)
			if !ok {
				return failf("text_result_app_invalid", field("field", key))
			}
			if failure := p.set([]string{"app", key}, parsed); failure != nil {
				return failure
			}
		case "fg_restore":
			if !appForegroundRestoreSet[value] {
				return failf("text_result_app_invalid", field("field", key))
			}
			if failure := p.set([]string{"app", "foreground_restore"}, value); failure != nil {
				return failure
			}
		case "target_pids", "killed_pids", "remaining_pids":
			values, failure := parseCompactIntArray(value)
			if failure != nil {
				return failure
			}
			if failure := p.set([]string{"app", key}, values); failure != nil {
				return failure
			}
		case "total":
			number, ok := parseNonNegativeDecimal(value)
			if !ok {
				return failf("text_result_app_invalid", field("field", key))
			}
			if failure := p.set([]string{"app", "total"}, number); failure != nil {
				return failure
			}
		case "fg":
			if failure := p.set([]string{"app", "foreground"},
				parseForegroundToken(value)); failure != nil {
				return failure
			}
		case "at":
			if !validTimeToken(value) {
				return failf("text_result_app_invalid", field("field", key))
			}
			if failure := p.set([]string{"app", "captured_at"}, value); failure != nil {
				return failure
			}
		}
	}
	return nil
}

// decodePackageToken accepts both the prosaic bare token and the JSON string
// escape used only when a package would otherwise break the token grammar.
func decodePackageToken(value string) (string, *checkError) {
	if strings.HasPrefix(value, `"`) {
		return decodeJSONStringToken(value)
	}
	if !resultToken(value) {
		return "", failf("text_result_app_invalid", field("field", "pkg"))
	}
	return value, nil
}

func decodeJSONStringToken(value string) (string, *checkError) {
	decoded, failure := decodeJSONValue(value)
	if failure != nil {
		return "", failf("text_result_app_invalid", field("field", "pkg"))
	}
	text, ok := decoded.(string)
	if !ok {
		return "", failf("text_result_app_invalid", field("field", "pkg"))
	}
	return text, nil
}

func parseNullableBoolToken(value string) (any, bool) {
	switch value {
	case "true":
		return true, true
	case "false":
		return false, true
	case "null":
		return nil, true
	default:
		return nil, false
	}
}

func parseCompactIntArray(value string) ([]any, *checkError) {
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") ||
		strings.ContainsAny(value, " \t") {
		return nil, failf("text_result_app_pids_invalid")
	}
	decoded, failure := decodeJSONValue(value)
	if failure != nil {
		return nil, failure
	}
	items, ok := decoded.([]any)
	if !ok {
		return nil, failf("text_result_app_pids_invalid")
	}
	result := make([]any, 0, len(items))
	for _, item := range items {
		number, ok := exactInteger(item)
		if !ok || number < 0 {
			return nil, failf("text_result_app_pids_invalid")
		}
		result = append(result, int(number))
	}
	return result, nil
}

// parseSortedTaskIDArray accepts one compact task-id array from the kill head.
// Task IDs are positive and ascending; the empty array is a complete, known
// empty set and stays distinguishable from an omitted member.
func parseSortedTaskIDArray(value, name string) ([]any, *checkError) {
	values, failure := parseCompactIntArray(value)
	if failure != nil {
		return nil, failure
	}
	previous := 0
	for _, item := range values {
		number, _ := item.(int)
		if number <= previous {
			return nil, failf("text_result_app_invalid", field("field", name))
		}
		previous = number
	}
	return values, nil
}

// decodeAppBlockFallback accepts an unknown member of one collection element
// emitted inside its formal block. The pointer must stay under that collection.
func (p *v4Parser) decodeAppBlockFallback(line, collection string) *checkError {
	path, value, failure := decodeJSONFallback(line)
	if failure != nil {
		return failure
	}
	if path != collection && !strings.HasPrefix(path, collection+"/") {
		return failf("text_result_app_block_fallback_invalid")
	}
	return setJSONPointer(p.object, p.seen, path, value)
}

func (p *v4Parser) decodeAppItems(lines []string, start int) (int, *checkError) {
	fields, failure := decodeBlockHeader(lines[start], "app_items v1 ")
	if failure != nil {
		return 0, failure
	}
	kind := fields["kind"]
	if kind != "find" && kind != "list" {
		return 0, failf("text_result_app_items_kind_invalid")
	}
	count, ok := parseNonNegativeDecimal(fields["count"])
	if !ok {
		return 0, failf("text_result_app_items_count_invalid")
	}
	appKind, found := lookupPath(p.object, []string{"app", "kind"})
	if !found || appKind != kind {
		return 0, failf("text_result_app_items_kind_mismatch")
	}
	items := make([]any, 0, count)
	fallbacks := make([]string, 0)
	end := start + 1
	for ; end < len(lines); end++ {
		line := lines[end]
		if line == "end app_items" {
			if failure := p.set([]string{"app", "items"}, items); failure != nil {
				return 0, failure
			}
			for _, fallback := range fallbacks {
				if failure := p.decodeAppBlockFallback(fallback, "/app/items"); failure != nil {
					return 0, failure
				}
			}
			actual := len(items)
			if stored, found := lookupPath(p.object, []string{"app", "items"}); found {
				if values, ok := stored.([]any); ok {
					actual = len(values)
				}
			}
			if actual != count {
				return 0, failf("text_result_app_items_count_mismatch",
					field("header", strconv.Itoa(count)), field("body", strconv.Itoa(actual)))
			}
			return end + 1, nil
		}
		if line == "" || strings.HasPrefix(line, " ") || strings.ContainsRune(line, '\t') {
			return 0, failf("text_result_app_items_invalid")
		}
		if strings.HasPrefix(line, "json v1 ") {
			fallbacks = append(fallbacks, line)
			continue
		}
		tokens, ok := treeLineTokens(line)
		if !ok || len(tokens) == 0 {
			return 0, failf("text_result_app_items_invalid")
		}
		switch kind {
		case "find":
			if len(tokens) != 2 || !resultToken(tokens[0]) || !isTreeJSONString(tokens[1]) {
				return 0, failf("text_result_app_items_invalid")
			}
			name, failure := decodeJSONValue(tokens[1])
			if failure != nil {
				return 0, failure
			}
			items = append(items, map[string]any{"package": tokens[0], "name": name})
		case "list":
			if len(tokens) < 3 || len(tokens) > 4 || !strings.HasPrefix(tokens[0], "#") {
				return 0, failf("text_result_app_items_invalid")
			}
			taskID, ok := parsePositiveDecimal(strings.TrimPrefix(tokens[0], "#"))
			if !ok || !resultToken(tokens[1]) || !appOwnershipSet[tokens[2]] {
				return 0, failf("text_result_app_items_invalid")
			}
			item := map[string]any{
				"task_id": taskID, "package": tokens[1], "ownership": tokens[2],
			}
			if len(tokens) == 4 {
				key, value, found := strings.Cut(tokens[3], "=")
				display, ok := parseNonNegativeDecimal(value)
				if !found || key != "origin_display" || !ok {
					return 0, failf("text_result_app_items_invalid")
				}
				item["origin_display"] = display
			}
			items = append(items, item)
		}
	}
	return 0, failf("text_result_app_items_unterminated")
}

func (p *v4Parser) decodeAppTasks(lines []string, start int) (int, *checkError) {
	fields, failure := decodeBlockHeader(lines[start], "app_tasks v1 ")
	if failure != nil {
		return 0, failure
	}
	count, ok := parseNonNegativeDecimal(fields["count"])
	if !ok || fields["kind"] != "" {
		return 0, failf("text_result_app_tasks_count_invalid")
	}
	appKind, found := lookupPath(p.object, []string{"app", "kind"})
	if !found || appKind != "close" {
		return 0, failf("text_result_app_tasks_kind_mismatch")
	}
	tasks := make([]any, 0, count)
	fallbacks := make([]string, 0)
	end := start + 1
	for ; end < len(lines); end++ {
		line := lines[end]
		if line == "end app_tasks" {
			if failure := p.set([]string{"app", "tasks"}, tasks); failure != nil {
				return 0, failure
			}
			for _, fallback := range fallbacks {
				if failure := p.decodeAppBlockFallback(fallback, "/app/tasks"); failure != nil {
					return 0, failure
				}
			}
			actual := len(tasks)
			if stored, found := lookupPath(p.object, []string{"app", "tasks"}); found {
				if values, ok := stored.([]any); ok {
					actual = len(values)
				}
			}
			if actual != count {
				return 0, failf("text_result_app_tasks_count_mismatch",
					field("header", strconv.Itoa(count)), field("body", strconv.Itoa(actual)))
			}
			return end + 1, nil
		}
		if line == "" || strings.HasPrefix(line, " ") || strings.ContainsRune(line, '\t') {
			return 0, failf("text_result_app_tasks_invalid")
		}
		if strings.HasPrefix(line, "json v1 ") {
			fallbacks = append(fallbacks, line)
			continue
		}
		tokens, ok := treeLineTokens(line)
		if !ok || len(tokens) < 2 || !strings.HasPrefix(tokens[0], "#") {
			return 0, failf("text_result_app_tasks_invalid")
		}
		taskID, ok := parsePositiveDecimal(strings.TrimPrefix(tokens[0], "#"))
		if !ok || !resultToken(tokens[1]) {
			return 0, failf("text_result_app_tasks_invalid")
		}
		task := map[string]any{"task_id": taskID, "outcome": tokens[1]}
		index := 2
		if index < len(tokens) && strings.HasPrefix(tokens[index], "recents_cleaned=") {
			value, ok := parseNullableBoolToken(strings.TrimPrefix(tokens[index], "recents_cleaned="))
			if !ok {
				return 0, failf("text_result_app_tasks_invalid")
			}
			task["recents_cleaned"] = value
			index++
		}
		if index < len(tokens) && strings.HasPrefix(tokens[index], "error=") {
			code := strings.TrimPrefix(tokens[index], "error=")
			if !resultToken(code) || index+1 >= len(tokens) || !isTreeJSONString(tokens[index+1]) {
				return 0, failf("text_result_app_tasks_invalid")
			}
			message, failure := decodeJSONValue(tokens[index+1])
			if failure != nil {
				return 0, failure
			}
			errorObject := map[string]any{"code": code, "message": message}
			index += 2
			if index < len(tokens) && strings.HasPrefix(tokens[index], "details=") {
				details, failure := decodeJSONValue(strings.TrimPrefix(tokens[index], "details="))
				if failure != nil {
					return 0, failure
				}
				errorObject["details"] = details
				index++
			}
			task["error"] = errorObject
		}
		if index != len(tokens) {
			return 0, failf("text_result_app_tasks_invalid")
		}
		tasks = append(tasks, task)
	}
	return 0, failf("text_result_app_tasks_unterminated")
}

func (p *v4Parser) decodeWait(line string) *checkError {
	tokens, failure := v4Tokens(line, "wait ")
	if failure != nil {
		return failure
	}
	if len(tokens) == 0 {
		return failf("text_result_wait_invalid")
	}
	order := map[string]int{"trigger": 0, "matched": 1, "elapsed": 2}
	last := -1
	var trigger string
	var elapsed int64
	haveElapsed := false
	for _, token := range tokens {
		key, value, found := strings.Cut(token, "=")
		rank, known := order[key]
		if !found || !known || rank <= last || value == "" {
			return failf("text_result_wait_invalid")
		}
		last = rank
		switch key {
		case "trigger":
			if !resultToken(value) {
				return failf("text_result_wait_invalid")
			}
			trigger = value
		case "matched":
			matched := make([]any, 0)
			for _, item := range strings.Split(value, ",") {
				if !resultToken(item) {
					return failf("text_result_wait_invalid")
				}
				matched = append(matched, item)
			}
			if failure := p.set([]string{"wait", "matched"}, matched); failure != nil {
				return failure
			}
		case "elapsed":
			if !strings.HasSuffix(value, "ms") {
				return failf("text_result_wait_invalid")
			}
			parsed, err := strconv.ParseInt(strings.TrimSuffix(value, "ms"), 10, 64)
			if err != nil || parsed < 0 {
				return failf("text_result_wait_invalid")
			}
			elapsed = parsed
			haveElapsed = true
		}
	}
	if trigger == "" || !haveElapsed {
		return failf("text_result_wait_invalid")
	}
	if failure := p.set([]string{"wait", "trigger"}, trigger); failure != nil {
		return failure
	}
	return p.set([]string{"wait", "elapsed_ms"}, elapsed)
}

func (p *v4Parser) decodeDiagnosis(line string) *checkError {
	tokens, failure := v4Tokens(line, "diagnosis ")
	if failure != nil {
		return failure
	}
	order := map[string]int{"summary": 0, "coverage": 1, "truncated": 2, "op": 3, "outcome": 4}
	last := -1
	seen := make(map[string]bool, 5)
	for _, token := range tokens {
		key, value, found := strings.Cut(token, "=")
		rank, known := order[key]
		if !found || !known || rank <= last || value == "" {
			return failf("text_result_diagnosis_invalid")
		}
		last = rank
		seen[key] = true
		switch key {
		case "summary":
			if !isTreeJSONString(value) {
				return failf("text_result_diagnosis_invalid", field("field", "summary"))
			}
			text, failure := decodeJSONValue(value)
			if failure != nil {
				return failure
			}
			if failure := p.set([]string{"diagnosis", "summary"}, text); failure != nil {
				return failure
			}
		case "coverage":
			if !resultToken(value) {
				return failf("text_result_diagnosis_invalid", field("field", key))
			}
			if failure := p.set([]string{"diagnosis", "coverage"}, value); failure != nil {
				return failure
			}
		case "truncated":
			if value != "true" && value != "false" {
				return failf("text_result_diagnosis_invalid", field("field", key))
			}
			if failure := p.set([]string{"diagnosis", "truncated"}, value == "true"); failure != nil {
				return failure
			}
		case "op":
			if !validReference(value, 'P') {
				return failf("text_result_diagnosis_invalid", field("field", key))
			}
			if failure := p.set([]string{"diagnosis", "operation_id"}, value); failure != nil {
				return failure
			}
		case "outcome":
			if !resultToken(value) {
				return failf("text_result_diagnosis_invalid", field("field", key))
			}
			if failure := p.set([]string{"diagnosis", "outcome"}, value); failure != nil {
				return failure
			}
		}
	}
	if !seen["summary"] || !seen["coverage"] || !seen["truncated"] {
		return failf("text_result_diagnosis_invalid")
	}
	return nil
}

func (p *v4Parser) decodeCleanup(line string) *checkError {
	tokens, failure := v4Tokens(line, "cleanup ")
	if failure != nil {
		return failure
	}
	if len(tokens) == 0 {
		return failf("text_result_cleanup_invalid")
	}
	if !resultToken(tokens[0]) {
		return failf("text_result_cleanup_invalid")
	}
	if failure := p.set([]string{"cleanup", "state"}, tokens[0]); failure != nil {
		return failure
	}
	order := map[string]int{"exec": 0, "tasks": 1, "recents": 2, "display": 3}
	last := -1
	for _, token := range tokens[1:] {
		key, value, found := strings.Cut(token, "=")
		rank, known := order[key]
		if !found || !known || rank <= last || !resultToken(value) {
			return failf("text_result_cleanup_invalid")
		}
		last = rank
		target := map[string]string{
			"exec": "execution", "tasks": "tasks", "recents": "recents", "display": "display",
		}[key]
		if failure := p.set([]string{"cleanup", target}, value); failure != nil {
			return failure
		}
	}
	return nil
}

func (p *v4Parser) decodeRecovery(line string) *checkError {
	tokens, failure := v4Tokens(line, "recovery ")
	if failure != nil {
		return failure
	}
	order := map[string]int{"entry": 0, "ref": 1, "input": 2, "cause": 3}
	last := -1
	values := make(map[string]string, 4)
	for _, token := range tokens {
		key, value, found := strings.Cut(token, "=")
		rank, known := order[key]
		if !found || !known || rank <= last || value == "" {
			return failf("text_result_recovery_invalid")
		}
		last = rank
		values[key] = value
	}
	entry := values["entry"]
	ref := values["ref"]
	if !resultToken(entry) || ref == "" {
		return failf("text_result_recovery_invalid")
	}
	if ref != "none" && ref != "O" && ref != "P" &&
		!validReference(ref, 'O') && !validReference(ref, 'P') {
		return failf("text_result_recovery_invalid", field("ref", ref))
	}
	if failure := p.set([]string{"recovery", "entry"}, entry); failure != nil {
		return failure
	}
	if failure := p.set([]string{"recovery", "reference"}, ref); failure != nil {
		return failure
	}
	if value := values["input"]; value != "" {
		if !resultToken(value) {
			return failf("text_result_recovery_invalid")
		}
		if failure := p.set([]string{"recovery", "input_state"}, value); failure != nil {
			return failure
		}
	}
	if value := values["cause"]; value != "" {
		if !resultToken(value) {
			return failf("text_result_recovery_invalid")
		}
		if failure := p.set([]string{"recovery", "cause"}, value); failure != nil {
			return failure
		}
	}
	return nil
}

// finish applies the explicit-omission rules that need the complete body.
func (p *v4Parser) finish() *checkError {
	if _, present := p.object["effect"]; present {
		return failf("text_result_effect_present")
	}
	if observation, ok := p.object["observation"].(map[string]any); ok {
		if _, present := observation["quality"]; present {
			return failf("text_result_obs_quality_present")
		}
	}
	// Only a non-error, executed, stateless result is represented by the plain
	// `ok` header. An error result always keeps its outcome explicit.
	if p.header.HasOutcome && p.header.Ok && p.header.State == "" &&
		p.header.Outcome == outcomeExecuted {
		return failf("text_result_outcome_redundant")
	}
	if !p.haveObs {
		if failure := p.checkTreeObservationOwner(); failure != nil {
			return failure
		}
		return resolveTreeBindings(p.object, p.seen, p.obsIDPresent, p.bindings)
	}
	screenValue, hasScreen := lookupPath(p.object, []string{"observation", "screen"})
	screen, screenIsObject := screenValue.(map[string]any)
	if hasScreen && screenIsObject {
		_, hasWidth := screen["width"]
		if hasWidth && p.obsScreenTok && !p.sessionLive {
			if _, hasTree := lookupPath(p.object, []string{"observation", "tree"}); hasTree {
				return failf("text_result_obs_screen_redundant")
			}
		}
	}
	if failure := p.checkTreeObservationOwner(); failure != nil {
		return failure
	}
	return resolveTreeBindings(p.object, p.seen, p.obsIDPresent, p.bindings)
}

// checkTreeObservationOwner proves a tree block belongs to the observation
// frame and that its short base can be expanded from the obs line id.
func (p *v4Parser) checkTreeObservationOwner() *checkError {
	if len(p.bindings) == 0 {
		return nil
	}
	if !p.haveObs {
		if _, found := lookupPath(p.object, []string{"observation", "id"}); found {
			return failf("text_tree_id_group_required")
		}
		return failf("text_tree_id_missing")
	}
	if !p.obsIDPresent {
		return failf("text_tree_id_group_required")
	}
	return nil
}
