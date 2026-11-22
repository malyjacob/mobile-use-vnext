package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func decodeResultJSON(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("second JSON value")
		}
		return nil, fmt.Errorf("trailing JSON: %w", err)
	}
	return v, nil
}

// decodeResultText decodes the current result v4 envelope. The legacy result v3
// decoder remains available for historical discovery fixtures that have not yet
// migrated; the production encoder only emits v4.
func decodeResultText(t *testing.T, text string) (map[string]any, bool, error) {
	t.Helper()
	if strings.HasPrefix(text, "result v3 is_error=") {
		return decodeResultTextV3(t, text)
	}
	return decodeResultTextV4(t, text)
}

// ---------------------------------------------------------------- result v4 decoder

// decodeResultTextV4 is an independent, test-only oracle for the compact v4
// syntax. It reconstructs the projected facts (never calls the encoder) and
// applies only the documented defaults: an obs line without status is the
// omitted "complete" default.
func decodeResultTextV4(t *testing.T, text string) (map[string]any, bool, error) {
	t.Helper()
	bad := func() (map[string]any, bool, error) { return nil, false, fmt.Errorf("invalid result v4 text") }
	if text == "" || strings.ContainsRune(text, '\r') ||
		!strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
		return nil, false, fmt.Errorf("result text must be non-empty LF text with one trailer")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) < 2 || lines[len(lines)-1] != "end result" {
		return bad()
	}
	header := strings.Fields(lines[0])
	if len(header) < 3 || header[0] != "result" || header[1] != "v4" ||
		(header[2] != "ok" && header[2] != "err") {
		return bad()
	}
	isError := header[2] == "err"
	root := map[string]any{}
	seen := map[string]bool{}
	put := func(path string, value any) error {
		parts, err := decodeJSONPointer(path)
		if err != nil {
			return err
		}
		if seen[path] {
			return fmt.Errorf("duplicate path %s", path)
		}
		seen[path] = true
		var set func(any, []string) (any, error)
		set = func(node any, parts []string) (any, error) {
			if len(parts) == 0 {
				return value, nil
			}
			if node == nil {
				node = map[string]any{}
			}
			n, ok := node.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid pointer parent")
			}
			child, err := set(n[parts[0]], parts[1:])
			if err != nil {
				return nil, err
			}
			n[parts[0]] = child
			return n, nil
		}
		_, err = set(root, parts)
		return err
	}
	putJSON := func(path string, raw []byte) error {
		value, err := decodeResultJSON(raw)
		if err != nil {
			return err
		}
		return put(path, value)
	}
	for _, token := range header[3:] {
		key, value, ok := strings.Cut(token, "=")
		if !ok || key == "" || value == "" {
			return bad()
		}
		switch key {
		case "op":
			if err := put("/operation_id", value); err != nil {
				return nil, false, err
			}
		case "state":
			if err := put("/operation_state", value); err != nil {
				return nil, false, err
			}
		case "outcome":
			if err := put("/outcome", value); err != nil {
				return nil, false, err
			}
		case "phase":
			if err := put("/phase", value); err != nil {
				return nil, false, err
			}
		default:
			return bad()
		}
	}
	observationID := ""
	previousBlank := false
	for index := 1; index < len(lines)-1; index++ {
		line := lines[index]
		if line == "" {
			if previousBlank {
				return nil, false, fmt.Errorf("repeated blank line")
			}
			previousBlank = true
			continue
		}
		previousBlank = false
		if strings.HasSuffix(line, " ") || strings.ContainsAny(line, "\t\r") {
			return nil, false, fmt.Errorf("non-canonical whitespace")
		}
		if strings.HasPrefix(line, "json v1 path=") {
			d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, "json v1 path=")))
			var path string
			if d.Decode(&path) != nil || !strings.HasPrefix(path, "/") {
				return bad()
			}
			rest := strings.TrimPrefix(line, "json v1 path=")[d.InputOffset():]
			value, ok := strings.CutPrefix(rest, " value=")
			if !ok || value == "" {
				return bad()
			}
			if err := putJSON(path, []byte(value)); err != nil {
				return nil, false, err
			}
			continue
		}
		if isResultV4BlockStart(line) {
			end, err := decodeResultV4Block(line, lines[index+1:], root, put)
			if err != nil {
				return nil, false, err
			}
			index += end
			continue
		}
		switch {
		case strings.HasPrefix(line, "obs "):
			if err := decodeResultV4ObservationLine(line, root); err != nil {
				return nil, false, err
			}
			observationID = resultObservationIDFromMap(root)
		case line == "obs.image":
			if err := put("/observation/image", map[string]any{}); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "obs.image "):
			image, err := decodeResultV4ImageLine(strings.TrimPrefix(line, "obs.image "))
			if err != nil {
				return nil, false, err
			}
			if err := put("/observation/image", image); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "obs.image.error "):
			errorValue, err := decodeResultV4ErrorLine(strings.TrimPrefix(line, "obs.image.error "))
			if err != nil {
				return nil, false, err
			}
			if err := put("/observation/image/error", errorValue); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "obs.error "):
			errorValue, err := decodeResultV4ErrorLine(strings.TrimPrefix(line, "obs.error "))
			if err != nil {
				return nil, false, err
			}
			if err := put("/observation/error", errorValue); err != nil {
				return nil, false, err
			}
		case line == "image":
			if err := put("/image", map[string]any{}); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "image "):
			image, err := decodeResultV4ImageLine(strings.TrimPrefix(line, "image "))
			if err != nil {
				return nil, false, err
			}
			if err := put("/image", image); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "image.error "):
			errorValue, err := decodeResultV4ErrorLine(strings.TrimPrefix(line, "image.error "))
			if err != nil {
				return nil, false, err
			}
			if err := put("/image/error", errorValue); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "notify.error.details="):
			if err := putJSON("/notification/error/details", []byte(strings.TrimPrefix(line, "notify.error.details="))); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "obs.image.error.details="):
			if err := putJSON("/observation/image/error/details", []byte(strings.TrimPrefix(line, "obs.image.error.details="))); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "obs.error.details="):
			if err := putJSON("/observation/error/details", []byte(strings.TrimPrefix(line, "obs.error.details="))); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "image.error.details="):
			if err := putJSON("/image/error/details", []byte(strings.TrimPrefix(line, "image.error.details="))); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "cleanup.error.details="):
			if err := putJSON("/cleanup/error/details", []byte(strings.TrimPrefix(line, "cleanup.error.details="))); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "notify.error "):
			errorValue, err := decodeResultV4ErrorLine(strings.TrimPrefix(line, "notify.error "))
			if err != nil {
				return nil, false, err
			}
			if err := put("/notification/error", errorValue); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "cleanup.error "):
			errorValue, err := decodeResultV4ErrorLine(strings.TrimPrefix(line, "cleanup.error "))
			if err != nil {
				return nil, false, err
			}
			if err := put("/cleanup/error", errorValue); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "error "):
			errorValue, err := decodeResultV4ErrorLine(strings.TrimPrefix(line, "error "))
			if err != nil {
				return nil, false, err
			}
			if err := put("/error", errorValue); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "error.details="):
			if err := putJSON("/error/details", []byte(strings.TrimPrefix(line, "error.details="))); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "notify "):
			if err := decodeResultV4NotifyLine(line, put); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "session "):
			if err := decodeResultV4SessionLine(line, put); err != nil {
				return nil, false, err
			}
		case line == "session=null":
			if err := put("/session", nil); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "app "):
			if err := decodeResultV4AppLine(line, put); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "wait "):
			if err := decodeResultV4WaitLine(line, put); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "diagnosis "):
			if err := decodeResultV4DiagnosisLine(line, put); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "diagnosis.issues="):
			if err := putJSON("/diagnosis/issues", []byte(strings.TrimPrefix(line, "diagnosis.issues="))); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "diagnosis.timing_ms="):
			if err := putJSON("/diagnosis/timing_ms", []byte(strings.TrimPrefix(line, "diagnosis.timing_ms="))); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "cleanup "):
			if err := decodeResultV4CleanupLine(line, put); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "recovery "):
			if err := decodeResultV4RecoveryLine(line, put); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "effect="):
			if err := put("/effect", map[string]any{"state": strings.TrimPrefix(line, "effect=")}); err != nil {
				return nil, false, err
			}
		case strings.HasPrefix(line, "text_input "):
			fields, err := decodeResultV4SimpleFields(strings.TrimPrefix(line, "text_input "))
			if err != nil {
				return nil, false, err
			}
			if err := put("/text_input", map[string]any{"write": fields["write"], "submit": fields["submit"]}); err != nil {
				return nil, false, err
			}
		default:
			key, value, ok := strings.Cut(line, "=")
			if !ok || key == "" || value == "" {
				return bad()
			}
			switch key {
			case "target_reference", "request_closed", "blocking_operation_id", "can_start",
				"blocking_code", "start_operation_id", "observation_state", "input_state":
				if err := decodeResultV4ScalarField("/"+key, key, value, put); err != nil {
					return nil, false, err
				}
			default:
				return bad()
			}
		}
	}
	// Apply the documented default that an emitted obs line implies status
	// complete when the status token was omitted.
	if observation, ok := root["observation"].(map[string]any); ok {
		if _, present := observation["status"]; !present {
			observation["status"] = "complete"
		}
	}
	_ = observationID
	return root, isError, nil
}

func resultObservationIDFromMap(root map[string]any) string {
	observation, _ := root["observation"].(map[string]any)
	id, _ := observation["id"].(string)
	return id
}

func isResultV4BlockStart(line string) bool {
	for _, prefix := range []string{
		"tree v4 ", "inspect v3 ", "shell v1 ", "timeline v1 ", "app_items v1 ", "app_tasks v1 ",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// decodeResultV4Block parses one block and inserts its facts. It returns the
// number of following lines consumed.
func decodeResultV4Block(line string, rest []string, root map[string]any, put func(string, any) error) (int, error) {
	kind := strings.Fields(line)[0]
	end := "end " + kind
	consumed := 0
	for consumed < len(rest) && rest[consumed] != end {
		consumed++
	}
	if consumed >= len(rest) {
		return 0, fmt.Errorf("unterminated %s block", kind)
	}
	block := strings.Join(append([]string{line}, rest[:consumed+1]...), "\n") + "\n"
	switch kind {
	case "tree":
		observation, _ := root["observation"].(map[string]any)
		id, _ := observation["id"].(string)
		if id == "" {
			return 0, fmt.Errorf("tree block has no observation id")
		}
		var value any
		var err error
		switch {
		case strings.HasPrefix(line, "tree v4 full "):
			value, err = parseFullTreeTextValue(block, id)
		case strings.HasPrefix(line, "tree v4 delta "):
			value, err = parseDeltaTextValue(block, id)
		default:
			return 0, fmt.Errorf("unknown tree block")
		}
		if err != nil {
			return 0, err
		}
		raw, err := protocol.JSON(value)
		if err != nil {
			return 0, err
		}
		if err := put("/observation/tree", mustDecodeJSONValue(raw)); err != nil {
			return 0, err
		}
	case "inspect":
		value, err := parseInspectionTextValue(block)
		if err != nil {
			return 0, err
		}
		raw, err := protocol.JSON(value)
		if err != nil {
			return 0, err
		}
		object, _ := decodeResultJSON(raw)
		fields := object.(map[string]any)
		// inspection.origin is omitted by result v4. Restore presence with an
		// empty value so protocol.Decode can still admit discovery examples.
		fields["origin"] = ""
		for key, fieldValue := range fields {
			if err := put("/inspection/"+key, fieldValue); err != nil {
				return 0, err
			}
		}
	case "shell":
		headerFields, header, order, err := parseHeaderFieldsValue(line)
		if err != nil {
			return 0, err
		}
		_ = headerFields
		if !reflect.DeepEqual(order, []string{"termination", "exit_code", "truncated", "output_bytes"}) {
			return 0, fmt.Errorf("shell header fields are invalid")
		}
		for _, key := range []string{"termination", "exit_code", "truncated"} {
			raw := header[key]
			if key == "termination" {
				raw = quoteResult(raw)
			}
			if err := putJSONValue("/shell/"+key, raw, put); err != nil {
				return 0, err
			}
		}
		output := ""
		chunks := 0
		for _, row := range rest[:consumed] {
			if s, ok := strings.CutPrefix(row, "output "); ok {
				var value string
				if json.Unmarshal([]byte(s), &value) != nil {
					return 0, fmt.Errorf("bad shell output")
				}
				output += value
				chunks++
			} else if s, ok := strings.CutPrefix(row, "output_error "); ok {
				if err := putJSONValue("/shell/output_error", s, put); err != nil {
					return 0, err
				}
			} else if path, raw, ok := parseResultV4FallbackRow(row); ok {
				if err := putJSONValue(path, string(raw), put); err != nil {
					return 0, err
				}
			} else {
				return 0, fmt.Errorf("bad shell row")
			}
		}
		if header["output_bytes"] == "null" {
			if chunks != 0 {
				return 0, fmt.Errorf("null shell output carried bytes")
			}
			if err := put("/shell/output", nil); err != nil {
				return 0, err
			}
		} else {
			size, err := strconv.Atoi(header["output_bytes"])
			if err != nil || size != len(output) || chunks == 0 {
				return 0, fmt.Errorf("bad shell output size")
			}
			if err := put("/shell/output", output); err != nil {
				return 0, err
			}
		}
	case "timeline":
		_, header, order, err := parseHeaderFieldsValue(line)
		if err != nil {
			return 0, err
		}
		if !reflect.DeepEqual(order, []string{"events"}) {
			return 0, fmt.Errorf("timeline header fields are invalid")
		}
		items := []any{}
		for _, row := range rest[:consumed] {
			s, ok := strings.CutPrefix(row, "event ")
			if !ok {
				return 0, fmt.Errorf("bad timeline row")
			}
			value, err := decodeResultJSON([]byte(s))
			if err != nil {
				return 0, err
			}
			items = append(items, value)
		}
		count, err := strconv.Atoi(header["events"])
		if err != nil || count != len(items) {
			return 0, fmt.Errorf("bad timeline count")
		}
		if err := put("/diagnosis/timeline", items); err != nil {
			return 0, err
		}
	case "app_items":
		if err := decodeResultV4AppItemsBlock(line, rest[:consumed], put); err != nil {
			return 0, err
		}
	case "app_tasks":
		if err := decodeResultV4AppTasksBlock(line, rest[:consumed], put); err != nil {
			return 0, err
		}
	}
	return consumed + 1, nil
}

func mustDecodeJSONValue(raw []byte) any {
	value, _ := decodeResultJSON(raw)
	return value
}

func putJSONValue(path, raw string, put func(string, any) error) error {
	value, err := decodeResultJSON([]byte(raw))
	if err != nil {
		return err
	}
	return put(path, value)
}

// parseResultV4FallbackRow decodes one json v1 row nested inside a block.
func parseResultV4FallbackRow(row string) (string, []byte, bool) {
	rest, ok := strings.CutPrefix(row, "json v1 path=")
	if !ok {
		return "", nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(rest))
	var path string
	if decoder.Decode(&path) != nil || !strings.HasPrefix(path, "/") {
		return "", nil, false
	}
	tail := rest[decoder.InputOffset():]
	value, ok := strings.CutPrefix(tail, " value=")
	if !ok || value == "" {
		return "", nil, false
	}
	return path, []byte(value), true
}

func decodeResultV4AppItemsBlock(line string, rows []string, put func(string, any) error) error {
	fields, err := decodeResultV4SimpleFields(strings.TrimPrefix(line, "app_items v1 "))
	if err != nil {
		return err
	}
	kind := fields["kind"]
	count, err := strconv.Atoi(fields["count"])
	if err != nil {
		return err
	}
	items := make([]any, 0, count)
	for _, row := range rows {
		if strings.HasPrefix(row, "json v1 path=") {
			return fmt.Errorf("app_items nested fallback is not supported by the oracle")
		}
		if kind == "find" {
			first := strings.IndexByte(row, ' ')
			if first <= 0 {
				return fmt.Errorf("bad app_items find row")
			}
			pkg := row[:first]
			var name string
			if json.Unmarshal([]byte(row[first+1:]), &name) != nil {
				return fmt.Errorf("bad app_items name")
			}
			items = append(items, map[string]any{"name": name, "package": pkg})
			continue
		}
		parts := strings.Fields(row)
		if len(parts) < 3 || !strings.HasPrefix(parts[0], "#") {
			return fmt.Errorf("bad app_items list row")
		}
		taskID, err := strconv.Atoi(strings.TrimPrefix(parts[0], "#"))
		if err != nil {
			return err
		}
		item := map[string]any{"task_id": taskID, "package": parts[1], "ownership": parts[2]}
		if len(parts) == 4 {
			origin, err := strconv.Atoi(strings.TrimPrefix(parts[3], "origin_display="))
			if err != nil {
				return err
			}
			item["origin_display"] = origin
		}
		items = append(items, item)
	}
	if len(items) != count {
		return fmt.Errorf("app_items count mismatch")
	}
	return put("/app/items", items)
}

func decodeResultV4AppTasksBlock(line string, rows []string, put func(string, any) error) error {
	fields, err := decodeResultV4SimpleFields(strings.TrimPrefix(line, "app_tasks v1 "))
	if err != nil {
		return err
	}
	count, err := strconv.Atoi(fields["count"])
	if err != nil {
		return err
	}
	tasks := make([]any, 0, count)
	for _, row := range rows {
		if !strings.HasPrefix(row, "#") {
			return fmt.Errorf("bad app_tasks row")
		}
		rest := strings.TrimPrefix(row, "#")
		first := strings.IndexByte(rest, ' ')
		if first <= 0 {
			return fmt.Errorf("bad app_tasks row")
		}
		taskID, err := strconv.Atoi(rest[:first])
		if err != nil {
			return err
		}
		rest = rest[first+1:]
		task := map[string]any{"task_id": taskID}
		outcome := rest
		if index := strings.Index(rest, " recents_cleaned="); index >= 0 {
			outcome = rest[:index]
			value := rest[index+len(" recents_cleaned="):]
			if next := strings.IndexByte(value, ' '); next >= 0 {
				value = value[:next]
			}
			switch value {
			case "true":
				task["recents_cleaned"] = true
			case "false":
				task["recents_cleaned"] = false
			case "null":
				task["recents_cleaned"] = nil
			default:
				return fmt.Errorf("bad recents_cleaned")
			}
		}
		task["outcome"] = outcome
		if index := strings.Index(rest, " error="); index >= 0 {
			tail := rest[index+len(" error="):]
			codeEnd := strings.IndexByte(tail, ' ')
			if codeEnd <= 0 {
				return fmt.Errorf("bad app_tasks error")
			}
			code := tail[:codeEnd]
			tail = tail[codeEnd+1:]
			detailsAt := strings.Index(tail, " details=")
			messagePart := tail
			var details any
			if detailsAt >= 0 {
				messagePart = tail[:detailsAt]
				details, err = decodeResultJSON([]byte(tail[detailsAt+len(" details="):]))
				if err != nil {
					return err
				}
			}
			var message string
			if json.Unmarshal([]byte(messagePart), &message) != nil {
				return fmt.Errorf("bad app_tasks message")
			}
			errorValue := map[string]any{"code": code, "message": message}
			if details != nil {
				errorValue["details"] = details
			}
			task["error"] = errorValue
			task["outcome"] = strings.TrimSuffix(outcome, " error")
		}
		tasks = append(tasks, task)
	}
	if len(tasks) != count {
		return fmt.Errorf("app_tasks count mismatch")
	}
	return put("/app/tasks", tasks)
}

func decodeResultV4ImageLine(body string) (map[string]any, error) {
	image := map[string]any{}
	screen := map[string]any{}
	for _, part := range strings.Fields(body) {
		switch {
		case part == "read_only":
			image["read_only"] = true
		case strings.HasPrefix(part, "media="):
			image["media_type"] = strings.TrimPrefix(part, "media=")
		case strings.HasPrefix(part, "screen="):
			value := strings.TrimPrefix(part, "screen=")
			at := strings.IndexByte(value, '@')
			size := value
			if at >= 0 {
				size = value[:at]
				display, err := strconv.Atoi(value[at+1:])
				if err != nil {
					return nil, err
				}
				screen["display_id"] = display
			}
			width, height, err := parseSizeToken(size)
			if err != nil {
				return nil, err
			}
			screen["width"] = width
			screen["height"] = height
			screen["rotation"] = 0
		case strings.HasPrefix(part, "rot="):
			rotation, err := strconv.Atoi(strings.TrimPrefix(part, "rot="))
			if err != nil {
				return nil, err
			}
			screen["rotation"] = rotation
		case strings.HasPrefix(part, "at="):
			image["captured_at"] = strings.TrimPrefix(part, "at=")
		case strings.Contains(part, "x"):
			width, height, err := parseSizeToken(part)
			if err != nil {
				return nil, err
			}
			image["width"] = width
			image["height"] = height
		default:
			return nil, fmt.Errorf("unknown image part %q", part)
		}
	}
	if len(screen) > 0 {
		image["screen"] = screen
	}
	return image, nil
}

func parseSizeToken(token string) (int, int, error) {
	index := strings.IndexByte(token, 'x')
	if index <= 0 {
		return 0, 0, fmt.Errorf("bad size token")
	}
	width, err := strconv.Atoi(token[:index])
	if err != nil {
		return 0, 0, err
	}
	height, err := strconv.Atoi(token[index+1:])
	if err != nil {
		return 0, 0, err
	}
	return width, height, nil
}

func decodeResultV4ErrorLine(body string) (map[string]any, error) {
	index := strings.IndexByte(body, ' ')
	if index <= 0 {
		return nil, fmt.Errorf("bad error line")
	}
	code := body[:index]
	messagePart := body[index+1:]
	var details any
	if detailsAt := strings.Index(messagePart, " details="); detailsAt >= 0 {
		var err error
		details, err = decodeResultJSON([]byte(messagePart[detailsAt+len(" details="):]))
		if err != nil {
			return nil, err
		}
		messagePart = messagePart[:detailsAt]
	}
	var message string
	if json.Unmarshal([]byte(messagePart), &message) != nil {
		return nil, fmt.Errorf("bad error message")
	}
	errorValue := map[string]any{"code": code, "message": message}
	if details != nil {
		errorValue["details"] = details
	}
	return errorValue, nil
}

func decodeResultV4SimpleFields(body string) (map[string]string, error) {
	fields := map[string]string{}
	for _, part := range strings.Fields(body) {
		key, value, ok := strings.Cut(part, "=")
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("bad field %q", part)
		}
		fields[key] = value
	}
	return fields, nil
}

func decodeResultV4ScalarField(path, key, value string, put func(string, any) error) error {
	switch key {
	case "request_closed", "can_start":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		return put(path, parsed)
	default:
		return put(path, value)
	}
}

func decodeResultV4NotifyLine(line string, put func(string, any) error) error {
	fields, err := decodeResultV4SimpleFields(strings.TrimPrefix(line, "notify "))
	if err != nil {
		return err
	}
	if value, ok := fields["id"]; ok {
		if err := put("/message_id", value); err != nil {
			return err
		}
	}
	if value, ok := fields["saved"]; ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		if err := put("/saved", parsed); err != nil {
			return err
		}
	}
	if value, ok := fields["state"]; ok {
		if err := put("/notification", map[string]any{"state": value}); err != nil {
			return err
		}
	}
	return nil
}

func decodeResultV4SessionLine(line string, put func(string, any) error) error {
	fields := strings.Fields(strings.TrimPrefix(line, "session "))
	if len(fields) == 0 {
		return fmt.Errorf("empty session")
	}
	session := map[string]any{"state": fields[0]}
	for _, part := range fields[1:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return fmt.Errorf("bad session field")
		}
		switch key {
		case "mode":
			session["mode"] = value
		case "origin":
			session["origin"] = value
		case "started":
			session["started_at"] = value
		case "expires":
			session["expires_at"] = value
		case "ended":
			session["ended_at"] = value
		case "reason":
			session["reason"] = value
		default:
			return fmt.Errorf("unknown session field %q", key)
		}
	}
	return put("/session", session)
}

func decodeResultV4ObservationLine(line string, root map[string]any) error {
	body := strings.TrimPrefix(line, "obs ")
	if body == "" {
		return fmt.Errorf("bare obs line")
	}
	observation := map[string]any{}
	for index, part := range strings.Fields(body) {
		if !strings.Contains(part, "=") {
			if index != 0 {
				return fmt.Errorf("misplaced observation id")
			}
			observation["id"] = part
			continue
		}
		key, value, _ := strings.Cut(part, "=")
		switch key {
		case "status":
			observation["status"] = value
		case "fg":
			foreground, err := decodeResultV4ForegroundToken(value)
			if err != nil {
				return err
			}
			observation["foreground"] = foreground
		case "at":
			observation["captured_at"] = value
		case "quality":
			quality, err := decodeResultV4QualityToken(value)
			if err != nil {
				return err
			}
			observation["quality"] = quality
		case "screen":
			scr := map[string]any{}
			screenValue := value
			if at := strings.IndexByte(screenValue, '@'); at >= 0 {
				display, err := strconv.Atoi(screenValue[at+1:])
				if err != nil {
					return err
				}
				scr["display_id"] = display
				screenValue = screenValue[:at]
			}
			width, height, err := parseSizeToken(screenValue)
			if err != nil {
				return err
			}
			scr["width"] = width
			scr["height"] = height
			scr["rotation"] = 0
			observation["screen"] = scr
		case "rot":
			rotation, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			scr, _ := observation["screen"].(map[string]any)
			if scr == nil {
				scr = map[string]any{}
				observation["screen"] = scr
			}
			scr["rotation"] = rotation
		case "missing":
			observation["missing"] = strings.Split(value, ",")
		default:
			return fmt.Errorf("unknown obs field %q", key)
		}
	}
	if existing, ok := root["observation"].(map[string]any); ok {
		for key, value := range observation {
			existing[key] = value
		}
		if _, present := existing["status"]; !present {
			existing["status"] = "complete"
		}
		return nil
	}
	if _, present := observation["status"]; !present {
		observation["status"] = "complete"
	}
	root["observation"] = observation
	return nil
}

func decodeResultV4ForegroundToken(token string) (map[string]any, error) {
	foreground := map[string]any{}
	switch {
	case token == "present":
		foreground["state"] = "present"
	case token == "none":
		foreground["state"] = "none"
	case strings.HasPrefix(token, "unknown(") && strings.HasSuffix(token, ")"):
		foreground["state"] = "unknown"
		foreground["reason"] = token[len("unknown(") : len(token)-1]
	case token == "unknown":
		foreground["state"] = "unknown"
	default:
		foreground["state"] = "present"
		if hash := strings.IndexByte(token, '#'); hash == 0 {
			taskID, err := strconv.Atoi(token[1:])
			if err != nil {
				return nil, err
			}
			foreground["task_id"] = taskID
		} else if hash > 0 {
			taskID, err := strconv.Atoi(token[hash+1:])
			if err != nil {
				return nil, err
			}
			foreground["package"] = token[:hash]
			foreground["task_id"] = taskID
		} else {
			foreground["package"] = token
		}
	}
	return foreground, nil
}

func decodeResultV4QualityToken(token string) (map[string]any, error) {
	quality := map[string]any{}
	if open := strings.IndexByte(token, '('); open >= 0 && strings.HasSuffix(token, ")") {
		quality["state"] = token[:open]
		quality["stop_reason"] = token[open+1 : len(token)-1]
		return quality, nil
	}
	quality["state"] = token
	return quality, nil
}

func decodeResultV4AppLine(line string, put func(string, any) error) error {
	fields := strings.Fields(strings.TrimPrefix(line, "app "))
	if len(fields) == 0 {
		return fmt.Errorf("empty app line")
	}
	app := map[string]any{"kind": fields[0]}
	for _, part := range fields[1:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return fmt.Errorf("bad app field")
		}
		switch key {
		case "pkg":
			app["package"] = value
		case "task":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			app["task_id"] = parsed
		case "src_task":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			app["source_task_id"] = parsed
		case "src_display":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			app["source_display_id"] = parsed
		case "display":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			app["display_id"] = parsed
		case "ownership":
			app["ownership"] = value
		case "reused":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return err
			}
			app["reused"] = parsed
		case "state_preserved", "restarted":
			if value == "null" {
				app[key] = nil
			} else {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return err
				}
				app[key] = parsed
			}
		case "fg_restore":
			app["foreground_restore"] = value
		case "complete":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return err
			}
			app["complete"] = parsed
		case "target_pids", "killed_pids", "remaining_pids":
			parsed, err := decodeResultJSON([]byte(value))
			if err != nil {
				return err
			}
			app[key] = parsed
		case "total":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			app["total"] = parsed
		case "truncated":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return err
			}
			app["truncated"] = parsed
		case "fg":
			foreground, err := decodeResultV4ForegroundToken(value)
			if err != nil {
				return err
			}
			app["foreground"] = foreground
		case "at":
			app["captured_at"] = value
		default:
			return fmt.Errorf("unknown app field %q", key)
		}
	}
	return put("/app", app)
}

func decodeResultV4WaitLine(line string, put func(string, any) error) error {
	fields := strings.Fields(strings.TrimPrefix(line, "wait "))
	wait := map[string]any{}
	for _, part := range fields {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return fmt.Errorf("bad wait field")
		}
		switch key {
		case "trigger":
			wait["trigger"] = value
		case "matched":
			wait["matched"] = strings.Split(value, ",")
		case "elapsed":
			elapsed, err := strconv.ParseInt(strings.TrimSuffix(value, "ms"), 10, 64)
			if err != nil {
				return err
			}
			wait["elapsed_ms"] = elapsed
		}
	}
	return put("/wait", wait)
}

func decodeResultV4DiagnosisLine(line string, put func(string, any) error) error {
	body := strings.TrimPrefix(line, "diagnosis ")
	diagnosis := map[string]any{}
	for len(body) > 0 {
		key, rest, ok := strings.Cut(body, "=")
		if !ok {
			return fmt.Errorf("bad diagnosis line")
		}
		key = strings.TrimSpace(key)
		switch key {
		case "summary":
			d := json.NewDecoder(strings.NewReader(rest))
			var summary string
			if err := d.Decode(&summary); err != nil {
				return err
			}
			diagnosis["summary"] = summary
			body = strings.TrimSpace(rest[d.InputOffset():])
		default:
			value, consumed := nextDiagnosisToken(rest)
			switch key {
			case "coverage":
				diagnosis["coverage"] = value
			case "truncated":
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return err
				}
				diagnosis["truncated"] = parsed
			case "op":
				diagnosis["operation_id"] = value
			case "outcome":
				diagnosis["outcome"] = value
			default:
				return fmt.Errorf("unknown diagnosis field %q", key)
			}
			body = strings.TrimSpace(rest[consumed:])
		}
	}
	return put("/diagnosis", diagnosis)
}

func nextDiagnosisToken(value string) (string, int) {
	index := strings.IndexByte(value, ' ')
	if index < 0 {
		return value, len(value)
	}
	return value[:index], index
}

func decodeResultV4CleanupLine(line string, put func(string, any) error) error {
	fields := strings.Fields(strings.TrimPrefix(line, "cleanup "))
	if len(fields) == 0 {
		return fmt.Errorf("empty cleanup line")
	}
	cleanup := map[string]any{"state": fields[0]}
	for _, part := range fields[1:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return fmt.Errorf("bad cleanup field")
		}
		switch key {
		case "exec":
			cleanup["execution"] = value
		case "tasks":
			cleanup["tasks"] = value
		case "recents":
			cleanup["recents"] = value
		case "display":
			cleanup["display"] = value
		default:
			return fmt.Errorf("unknown cleanup field %q", key)
		}
	}
	return put("/cleanup", cleanup)
}

func decodeResultV4RecoveryLine(line string, put func(string, any) error) error {
	fields, err := decodeResultV4SimpleFields(strings.TrimPrefix(line, "recovery "))
	if err != nil {
		return err
	}
	recovery := map[string]any{"entry": fields["entry"], "reference": fields["ref"]}
	if value, ok := fields["input"]; ok {
		recovery["input_state"] = value
	}
	if value, ok := fields["cause"]; ok {
		recovery["cause"] = value
	}
	return put("/recovery", recovery)
}

// ---------------------------------------------------------------- expected projection

// assertResultTextParity encodes result v4 and verifies the decoded projected
// facts equal the canonical result after the explicit omission table is
// applied. It is intentionally not a byte-for-byte round trip: result v4 is a
// model-facing projection, not a lossless carrier.
func assertResultTextParity(t *testing.T, raw []byte, isError bool) string {
	t.Helper()
	return assertResultTextProjection(t, raw, isError, "")
}

func assertResultTextProjection(t *testing.T, raw []byte, isError bool, requestRef string) string {
	t.Helper()
	text, err := encodeResultText(raw, isError, requestRef)
	if err != nil {
		t.Fatal(err)
	}
	decoded, flag, err := decodeResultText(t, text)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if flag != isError {
		t.Fatalf("is_error decoded as %v, want %v\n%s", flag, isError, text)
	}
	want := expectedResultV4(t, raw, isError, requestRef)
	if !sameJSONValue(want, decoded) {
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(decoded)
		t.Fatalf("projected facts mismatch\nwant=%s\ngot =%s\n%s", wantJSON, gotJSON, text)
	}
	again, err := encodeResultText(raw, isError, requestRef)
	if err != nil || text != again {
		t.Fatal("nondeterministic text")
	}
	return text
}

func sameJSONValue(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return sameResultJSON(left, right)
}

// expectedResultV4 applies the explicit omission table to a canonical result
// document. It is a test-only oracle and never calls the encoder.
func expectedResultV4(t *testing.T, raw []byte, isError bool, requestRef ...string) map[string]any {
	t.Helper()
	value, err := decodeResultJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %s", raw)
	}
	delete(root, "effect")
	if outcome, _ := root["outcome"].(string); !isError && outcome == "executed" {
		if state, _ := root["operation_state"].(string); state == "" {
			delete(root, "outcome")
		}
	}
	pruneResultV4Observation(root)
	pruneResultV4Session(root)
	pruneResultV4Image(root, "image")
	pruneResultV4Time(root, "captured_at")
	if inspection, ok := root["inspection"].(map[string]any); ok {
		// The inspect v3 block omits inspection.origin; the decoder restores
		// presence with an empty placeholder so protocol.Decode still admits
		// discovery examples.
		inspection["origin"] = ""
	}
	if app, ok := root["app"].(map[string]any); ok {
		pruneResultV4Time(app, "captured_at")
		pruneForegroundV4(app, "foreground")
	}
	if diagnosis, ok := root["diagnosis"].(map[string]any); ok {
		if timeline, present := diagnosis["timeline"].([]any); present && len(timeline) == 0 {
			delete(diagnosis, "timeline")
		}
	}
	if _, present := root["recovery"]; present {
		ref := ""
		if len(requestRef) > 0 {
			ref = requestRef[0]
		}
		rawMap := map[string]json.RawMessage{}
		for name, value := range root {
			encoded, err := json.Marshal(value)
			if err == nil {
				rawMap[name] = encoded
			}
		}
		if recovery, ok := root["recovery"].(map[string]any); ok {
			recovery["reference"] = resolveRecoveryReference(rawMap, ref)
		}
	}
	return root
}

func pruneResultV4Observation(root map[string]any) {
	observation, ok := root["observation"].(map[string]any)
	if !ok {
		return
	}
	delete(observation, "origin")
	delete(observation, "revision")
	delete(observation, "actionable_until")
	delete(observation, "quality")
	pruneResultV4Time(observation, "captured_at")
	pruneResultV4Image(observation, "image")
	pruneForegroundV4(observation, "foreground")
	session, hasSession := root["session"]
	hasSession = hasSession && session != nil
	tree, hasTree := observation["tree"]
	hasTree = hasTree && tree != nil
	if screen, ok := observation["screen"].(map[string]any); ok && !hasSession && hasTree {
		delete(screen, "width")
		delete(screen, "height")
		delete(screen, "display_id")
		if rotation, _ := screen["rotation"].(json.Number); rotation == "0" || rotation == "" {
			delete(observation, "screen")
		}
	}
}

func pruneForegroundV4(container map[string]any, name string) {
	foreground, ok := container[name].(map[string]any)
	if !ok {
		return
	}
	state, _ := foreground["state"].(string)
	switch state {
	case "present":
		if taskID, present := foreground["task_id"]; present && taskID == nil {
			delete(foreground, "task_id")
		}
		if pkg, present := foreground["package"].(string); present && pkg == "" {
			delete(foreground, "package")
		}
	case "unknown":
		if reason, present := foreground["reason"].(string); present && reason == "" {
			delete(foreground, "reason")
		}
	}
}

func pruneResultV4Image(container map[string]any, name string) {
	image, ok := container[name].(map[string]any)
	if !ok {
		return
	}
	if media, _ := image["media_type"].(string); media == "image/jpeg" || media == "" {
		delete(image, "media_type")
	}
	if readOnly, ok := image["read_only"].(bool); ok && !readOnly {
		delete(image, "read_only")
	}
	delete(image, "base64")
	pruneResultV4Time(image, "captured_at")
}

func pruneResultV4Session(root map[string]any) {
	session, ok := root["session"].(map[string]any)
	if !ok {
		return
	}
	delete(session, "agent")
	for _, field := range []string{"started_at", "expires_at", "ended_at"} {
		pruneResultV4Time(session, field)
	}
}

func pruneResultV4Time(container map[string]any, name string) {
	value, ok := container[name].(string)
	if !ok {
		return
	}
	formatted, ok := resultV4Time(value)
	if ok {
		container[name] = formatted
	}
}

// ---------------------------------------------------------------- legacy v3 decoder

// decodeResultTextV3 is retained for historical discovery fixtures only. It
// reconstructs the v3 lossless document exactly.
func decodeResultTextV3(t *testing.T, text string) (map[string]any, bool, error) {
	t.Helper()
	if text == "" || strings.ContainsRune(text, '\r') ||
		!strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
		return nil, false, fmt.Errorf("result text must be non-empty LF text with one trailer")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	bad := func() (map[string]any, bool, error) { return nil, false, fmt.Errorf("invalid result text") }
	if len(lines) < 2 || lines[len(lines)-1] != "end result" {
		return bad()
	}
	flag, ok := strings.CutPrefix(lines[0], "result v3 is_error=")
	if !ok || (flag != "true" && flag != "false") {
		return bad()
	}
	root := map[string]any{}
	seen := map[string]bool{}
	put := func(path string, v any) error {
		parts, err := decodeJSONPointer(path)
		if err != nil {
			return err
		}
		for old := range seen {
			if old == path || strings.HasPrefix(path, old+"/") || strings.HasPrefix(old, path+"/") {
				return fmt.Errorf("duplicate path %s", path)
			}
		}
		seen[path] = true
		var set func(any, []string) (any, error)
		set = func(node any, parts []string) (any, error) {
			if len(parts) == 0 {
				return v, nil
			}
			if node == nil {
				node = map[string]any{}
			}
			n, ok := node.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid pointer parent")
			}
			child, err := set(n[parts[0]], parts[1:])
			if err != nil {
				return nil, err
			}
			n[parts[0]] = child
			return n, nil
		}
		_, err = set(root, parts)
		return err
	}
	putJSON := func(path string, raw []byte) error {
		v, err := decodeResultJSON(raw)
		if err != nil {
			return err
		}
		return put(path, v)
	}
	groupOpen := false
	groupFields := 0
	closeGroup := func() error {
		if groupOpen && groupFields == 0 {
			return fmt.Errorf("observation group is empty")
		}
		groupOpen = false
		groupFields = 0
		return nil
	}
	putObservationField := func(relative string, value any) error {
		if relative == "" || strings.HasPrefix(relative, ".") || strings.HasSuffix(relative, ".") ||
			strings.Contains(relative, "..") {
			return fmt.Errorf("malformed observation subpath %q", relative)
		}
		path := "/observation/" + strings.ReplaceAll(relative, ".", "/")
		if err := put(path, value); err != nil {
			return err
		}
		groupFields++
		return nil
	}
	previousBlank := false
	for i := 1; i < len(lines)-1; i++ {
		line := lines[i]
		if line == "" {
			if err := closeGroup(); err != nil {
				return nil, false, err
			}
			if previousBlank {
				return nil, false, fmt.Errorf("repeated blank line")
			}
			previousBlank = true
			continue
		}
		previousBlank = false
		if strings.HasSuffix(line, " ") || strings.ContainsAny(line, "\t\r") {
			return nil, false, fmt.Errorf("line has non-canonical trailing or control whitespace")
		}
		if strings.HasPrefix(line, " ") {
			if !groupOpen {
				return nil, false, fmt.Errorf("indented line outside observation group")
			}
			key, value, ok := strings.Cut(strings.TrimPrefix(line, " "), "=")
			if !ok || key == "" || value == "" {
				return nil, false, fmt.Errorf("malformed observation group field")
			}
			kind, known := resultFields["observation."+key]
			if !known {
				return nil, false, fmt.Errorf("unknown observation field %q", key)
			}
			decoded, err := decodeResultFieldValue(value, kind)
			if err != nil {
				return nil, false, err
			}
			if err := putObservationField(key, decoded); err != nil {
				return nil, false, err
			}
			continue
		}
		if line == "observation" {
			if err := closeGroup(); err != nil {
				return nil, false, err
			}
			groupOpen = true
			continue
		}
		if strings.HasPrefix(line, "json v1 path=") {
			d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, "json v1 path=")))
			var path string
			if err := closeGroup(); err != nil {
				return nil, false, err
			}
			if d.Decode(&path) != nil || !strings.HasPrefix(path, "/") {
				return bad()
			}
			rest := strings.TrimPrefix(line, "json v1 path=")[d.InputOffset():]
			value, ok := strings.CutPrefix(rest, " value=")
			if !ok || value == "" {
				return bad()
			}
			if err := putJSON(path, []byte(value)); err != nil {
				return nil, false, err
			}
			continue
		}
		if isResultV4BlockStart(line) || strings.HasPrefix(line, "tree v3 ") {
			if err := closeGroup(); err != nil {
				return nil, false, err
			}
			end, err := decodeResultV4Block(line, lines[i+1:], root, put)
			if err != nil {
				return nil, false, err
			}
			i += end - 1
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		kind, known := resultFields[key]
		if !ok || !known || key == "" || value == "" {
			return bad()
		}
		if strings.HasPrefix(key, "observation.") {
			return nil, false, fmt.Errorf("observation field outside its group")
		}
		if err := closeGroup(); err != nil {
			return nil, false, err
		}
		v, err := decodeResultFieldValue(value, kind)
		if err != nil {
			return nil, false, err
		}
		if err := put("/"+strings.ReplaceAll(key, ".", "/"), v); err != nil {
			return nil, false, err
		}
	}
	if err := closeGroup(); err != nil {
		return nil, false, err
	}
	return root, flag == "true", nil
}

func decodeJSONPointer(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("JSON pointer %q is not absolute", path)
	}
	rawParts := strings.Split(path[1:], "/")
	parts := make([]string, len(rawParts))
	for index, raw := range rawParts {
		var decoded strings.Builder
		for cursor := 0; cursor < len(raw); cursor++ {
			if raw[cursor] != '~' {
				decoded.WriteByte(raw[cursor])
				continue
			}
			if cursor+1 >= len(raw) {
				return nil, fmt.Errorf("JSON pointer has an incomplete escape")
			}
			cursor++
			switch raw[cursor] {
			case '0':
				decoded.WriteByte('~')
			case '1':
				decoded.WriteByte('/')
			default:
				return nil, fmt.Errorf("JSON pointer has an invalid escape")
			}
		}
		parts[index] = decoded.String()
	}
	return parts, nil
}

func decodeResultFieldValue(value string, kind resultFieldKind) (any, error) {
	if kind == fieldBlock {
		return nil, fmt.Errorf("block field cannot use a scalar line")
	}
	if kind == fieldToken && !strings.HasPrefix(value, "\"") {
		if !resultToken(value) {
			return nil, fmt.Errorf("invalid bare token %q", value)
		}
		return value, nil
	}
	v, err := decodeResultJSON([]byte(value))
	if err != nil {
		return nil, err
	}
	if kind == fieldToken {
		if _, ok := v.(string); !ok {
			return nil, fmt.Errorf("token field is not a JSON string")
		}
	}
	if kind == fieldString {
		if _, ok := v.(string); !ok {
			return nil, fmt.Errorf("string field is not a JSON string")
		}
	}
	if kind == fieldLiteral {
		switch v.(type) {
		case nil, bool, json.Number:
		default:
			return nil, fmt.Errorf("literal field has a non-literal JSON value")
		}
	}
	if kind == fieldObject {
		switch v.(type) {
		case nil, map[string]any:
		default:
			return nil, fmt.Errorf("object field is not a JSON object or null")
		}
	}
	return v, nil
}
