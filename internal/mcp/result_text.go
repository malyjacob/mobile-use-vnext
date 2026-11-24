package mcp

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// resultV4FieldKind classifies one wire member for the result v4 encoder and
// the manifest completeness gate. The table is the single source of truth for
// "projected or explicitly omitted"; any new protocol.Result member must be
// added here (and to the omission table when it is deliberately not rendered).
type resultV4FieldKind uint8

const (
	v4Token   resultV4FieldKind = iota + 1 // bare token
	v4String                               // JSON double-quoted string payload
	v4JSON                                 // compact JSON value
	v4Object                               // dedicated object renderer
	v4Block                                // existing typed block (tree/inspect/shell/timeline)
	v4Literal                              // boolean / number / null literal
	v4Omitted                              // explicitly omitted by §4.2.2
)

// v4ResultFields is the complete classification of protocol.Result wire members.
var v4ResultFields = func() map[string]resultV4FieldKind {
	m := map[string]resultV4FieldKind{}
	add := func(prefix, names string, kind resultV4FieldKind) {
		for _, name := range strings.Fields(names) {
			m[prefix+name] = kind
		}
	}
	add("", "operation_id operation_state outcome phase target_reference request_closed blocking_operation_id can_start blocking_code start_operation_id observation_state input_state message_id saved", v4Token)
	add("", "text_input session cleanup error recovery observation app notification diagnosis", v4Object)
	add("", "effect", v4Omitted)
	add("effect.", "state", v4Token)
	add("text_input.", "write submit", v4Token)
	add("", "image inspection shell wait", v4Object)
	add("wait.", "trigger", v4Token)
	add("wait.", "matched", v4JSON)
	add("wait.", "elapsed_ms", v4Literal)
	add("session.", "origin state mode started_at expires_at ended_at reason", v4Token)
	add("session.", "agent", v4Omitted)
	add("cleanup.", "state execution tasks recents display", v4Token)
	add("error.", "code", v4Token)
	add("error.", "message", v4String)
	add("error.", "details", v4JSON)
	add("recovery.", "entry reference input_state cause", v4Token)
	add("observation.", "status id captured_at visual", v4Token)
	add("observation.", "origin revision actionable_until", v4Omitted)
	add("observation.", "foreground screen image error", v4Object)
	add("observation.", "quality", v4Omitted)
	add("observation.", "tree", v4Block)
	add("observation.", "missing", v4JSON)
	add("observation.quality.", "state stop_reason", v4Token)
	add("observation.quality.", "samples", v4Omitted)
	add("inspection.", "origin", v4Omitted)
	add("app.", "kind ownership foreground_restore captured_at", v4Token)
	add("app.", "package", v4String)
	add("app.", "task_id source_task_id display_id source_display_id total reused state_preserved complete truncated restarted", v4Literal)
	add("app.", "target_pids killed_pids remaining_pids", v4JSON)
	add("app.", "items tasks", v4Block)
	add("app.", "foreground", v4Object)
	add("notification.", "state", v4Token)
	add("notification.", "error", v4Object)
	add("cleanup.", "error", v4Object)
	add("diagnosis.", "summary", v4String)
	add("diagnosis.", "issues timing_ms", v4JSON)
	add("diagnosis.", "timeline", v4Block)
	add("diagnosis.", "coverage operation_id outcome", v4Token)
	add("diagnosis.", "truncated", v4Literal)
	add("", "request_closed can_start saved", v4Literal)
	add("", "inspection shell", v4Block)
	addImageV4Fields(m, "image.")
	addImageV4Fields(m, "observation.image.")
	addScreenV4Fields(m, "observation.screen.")
	addScreenV4Fields(m, "observation.image.screen.")
	addScreenV4Fields(m, "image.screen.")
	addErrorV4Fields(m, "error.")
	addErrorV4Fields(m, "observation.error.")
	addErrorV4Fields(m, "observation.image.error.")
	addErrorV4Fields(m, "image.error.")
	addErrorV4Fields(m, "notification.error.")
	addErrorV4Fields(m, "cleanup.error.")
	addForegroundV4Fields(m, "observation.foreground.")
	addForegroundV4Fields(m, "app.foreground.")
	return m
}()

// addForegroundV4Fields classifies one Foreground object at its wire prefix.
func addForegroundV4Fields(m map[string]resultV4FieldKind, prefix string) {
	m[prefix+"state"] = v4Token
	m[prefix+"package"] = v4Token
	m[prefix+"task_id"] = v4Literal
	m[prefix+"reason"] = v4Token
}

// addImageV4Fields classifies one ImageMeta object at its full wire prefix.
func addImageV4Fields(m map[string]resultV4FieldKind, prefix string) {
	for _, name := range strings.Fields("captured_at width height media_type read_only") {
		kind := v4Literal
		if name == "captured_at" || name == "media_type" {
			kind = v4Token
		}
		m[prefix+name] = kind
	}
	m[prefix+"screen"] = v4Object
	m[prefix+"error"] = v4Object
}

// addScreenV4Fields classifies one Screen object at prefix.
func addScreenV4Fields(m map[string]resultV4FieldKind, prefix string) {
	for _, name := range strings.Fields("width height rotation display_id") {
		m[prefix+name] = v4Literal
	}
}

// addErrorV4Fields classifies one Error object at prefix.
func addErrorV4Fields(m map[string]resultV4FieldKind, prefix string) {
	m[prefix+"code"] = v4Token
	m[prefix+"message"] = v4String
	m[prefix+"details"] = v4JSON
}

// v4OmittedResultFields lists members that result v4 never renders, with the
// reason required by the proposal's explicit omission table.
var v4OmittedResultFields = map[string]string{
	"effect":                       "model-facing text omits the evidence object; structured JSON retains it",
	"observation.origin":           "same-session constant; session.origin carries it",
	"observation.quality":          "model-facing text omits the sampling object; structured JSON retains it",
	"observation.revision":         "internal revision; no caller decision depends on it",
	"observation.actionable_until": "derived from captured_at plus a bounded deadline",
	"observation.quality.samples":  "audit-only sample count; structured JSON retains it",
	"session.agent":                "single fixed agent name",
	"inspection.origin":            "inspect header already names the observation",
}

type resultMember struct {
	name  string
	value json.RawMessage
}

// resultMembers reads members in wire order: Result's public order is
// significant for the readable projection; converting the input object to a
// map would lose it.
func resultMembers(raw []byte) ([]resultMember, error) {
	d := jsontext.NewDecoder(bytes.NewReader(raw))
	token, err := d.ReadToken()
	if err != nil || token.Kind() != '{' {
		return nil, fmt.Errorf("result object required")
	}
	var members []resultMember
	for d.PeekKind() != '}' {
		name, err := d.ReadToken()
		if err != nil {
			return nil, err
		}
		key := name.String()
		value, err := d.ReadValue()
		if err != nil {
			return nil, err
		}
		members = append(members, resultMember{key, bytes.Clone(value)})
	}
	_, err = d.ReadToken()
	return members, err
}

func resultToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c <= ' ' || c >= 127 || c == '=' || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

func compactResultJSON(raw []byte) string {
	v := jsontext.Value(bytes.Clone(raw))
	_ = v.Compact()
	return string(v)
}

func pointerPart(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func quoteResult(s string) string { b, _ := jsonv2.Marshal(s); return string(b) }

func rawIsNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func rawStringValue(raw []byte) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func rawBoolValue(raw []byte) (bool, bool) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

func rawIntValue(raw []byte) (int, bool) {
	var value int
	if json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	return value, true
}

func rawStringList(raw []byte) ([]string, bool) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return nil, false
	}
	return values, true
}

func rawIntList(raw []byte) ([]int, bool) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var values []int
	if json.Unmarshal(raw, &values) != nil {
		return nil, false
	}
	return values, true
}

func rawArrayValue(raw []byte) ([]json.RawMessage, bool) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, false
	}
	return values, true
}

// resultMemberMap indexes the ordered members and keeps the raw object.
func resultMemberMap(raw []byte) ([]resultMember, map[string]json.RawMessage, bool) {
	members, err := resultMembers(raw)
	if err != nil {
		return nil, nil, false
	}
	m := make(map[string]json.RawMessage, len(members))
	for _, member := range members {
		m[member.name] = member.value
	}
	return members, m, true
}

type resultV4Writer struct {
	strings.Builder
}

func (w *resultV4Writer) line(s string) {
	w.WriteString(s)
	w.WriteByte('\n')
}

// block always separates the block from the previous line with exactly one
// blank line. The block text itself already ends with a newline.
func (w *resultV4Writer) block(s string) {
	w.WriteByte('\n')
	w.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		w.WriteByte('\n')
	}
}

func (w *resultV4Writer) fallback(path string, raw []byte) {
	w.line("json v1 path=" + quoteResult(path) + " value=" + compactResultJSON(raw))
}

// resultV4Time truncates one RFC3339/RFC3339Nano timestamp to UTC HH:MM:SS.
// An unparseable value is kept verbatim only when it is a safe token; callers
// fall back to json v1 otherwise.
func resultV4Time(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Format("15:04:05"), true
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format("15:04:05"), true
	}
	if resultToken(value) {
		return value, true
	}
	return "", false
}

// encodeResultText renders the result v4 compact envelope. requestRef is the
// single-call request reference used only to resolve recovery refs to concrete
// O/P IDs; it never mutates the structured result.
func encodeResultText(raw []byte, isError bool, requestRef string) (string, error) {
	members, values, ok := resultMemberMap(raw)
	if !ok {
		return "", fmt.Errorf("result object required")
	}
	w := &resultV4Writer{}
	handled := map[string]bool{}

	// Header.
	header := "result v4 "
	if isError {
		header += "err"
	} else {
		header += "ok"
	}
	var headerFallbacks []resultMember
	headerToken := func(name, label string) {
		rawValue, present := values[name]
		if !present {
			return
		}
		handled[name] = true
		if value, valid := rawStringValue(rawValue); valid && resultToken(value) {
			header += " " + label + "=" + value
			return
		}
		headerFallbacks = append(headerFallbacks, resultMember{name, rawValue})
	}
	headerToken("operation_id", "op")
	operationState := ""
	if rawValue, present := values["operation_state"]; present {
		handled["operation_state"] = true
		if value, valid := rawStringValue(rawValue); valid && resultToken(value) {
			operationState = value
			header += " state=" + value
		} else {
			headerFallbacks = append(headerFallbacks, resultMember{"operation_state", rawValue})
		}
	}
	if rawValue, present := values["outcome"]; present {
		handled["outcome"] = true
		outcome, valid := rawStringValue(rawValue)
		omit := !isError && valid && outcome == "executed" && operationState == ""
		if omit {
			// ok + executed + no operation_state deterministically states the
			// conventional success; every other case keeps outcome explicit.
		} else if valid && resultToken(outcome) {
			header += " outcome=" + outcome
		} else {
			headerFallbacks = append(headerFallbacks, resultMember{"outcome", rawValue})
		}
	}
	headerToken("phase", "phase")
	w.line(header)
	for _, member := range headerFallbacks {
		w.fallback("/"+pointerPart(member.name), member.value)
	}

	// 1. Action and state control scalars, in fixed order.
	emitToken := func(name, label string) {
		rawValue, present := values[name]
		if !present {
			return
		}
		handled[name] = true
		if value, valid := rawStringValue(rawValue); valid && resultToken(value) {
			w.line(label + "=" + value)
			return
		}
		w.fallback("/"+pointerPart(name), rawValue)
	}
	if _, present := values["effect"]; present {
		handled["effect"] = true
	}
	if rawValue, present := values["text_input"]; present {
		handled["text_input"] = true
		if line, ok := renderTextInputV4(rawValue); ok {
			w.line(line)
		} else {
			w.fallback("/text_input", rawValue)
		}
	}
	emitToken("target_reference", "target_reference")
	if rawValue, present := values["request_closed"]; present {
		handled["request_closed"] = true
		if value, valid := rawBoolValue(rawValue); valid {
			w.line("request_closed=" + strconv.FormatBool(value))
		} else {
			w.fallback("/request_closed", rawValue)
		}
	}
	emitToken("blocking_operation_id", "blocking_operation_id")
	if rawValue, present := values["can_start"]; present {
		handled["can_start"] = true
		if value, valid := rawBoolValue(rawValue); valid {
			w.line("can_start=" + strconv.FormatBool(value))
		} else {
			w.fallback("/can_start", rawValue)
		}
	}
	emitToken("blocking_code", "blocking_code")
	emitToken("start_operation_id", "start_operation_id")
	emitToken("observation_state", "observation_state")
	emitToken("input_state", "input_state")

	// 2. notify aggregate line.
	renderNotifyV4(w, values, handled)

	// 3. Top-level snap image.
	if rawValue, present := values["image"]; present {
		handled["image"] = true
		if !renderImageV4(w, "image", rawValue) {
			w.fallback("/image", rawValue)
		}
	}

	// 4. Session.
	if rawValue, present := values["session"]; present {
		handled["session"] = true
		renderSessionV4(w, rawValue)
	}

	// 5. Observation line, children and tree block.
	if rawValue, present := values["observation"]; present {
		handled["observation"] = true
		renderObservationV4(w, rawValue, values)
	}

	// 6. Inspection block.
	if rawValue, present := values["inspection"]; present {
		handled["inspection"] = true
		if block, ok := readableResultBlock("inspection", rawValue, ""); ok {
			w.block(block)
		} else {
			w.fallback("/inspection", rawValue)
		}
	}

	// 7. App lifecycle.
	if rawValue, present := values["app"]; present {
		handled["app"] = true
		if !renderAppV4(w, rawValue) {
			w.fallback("/app", rawValue)
		}
	}

	// 8. Managed shell block.
	if rawValue, present := values["shell"]; present {
		handled["shell"] = true
		if block, ok := readableResultBlock("shell", rawValue, ""); ok {
			w.block(block)
		} else {
			w.fallback("/shell", rawValue)
		}
	}

	// 9. Condition wait.
	if rawValue, present := values["wait"]; present {
		handled["wait"] = true
		if !renderWaitV4(w, rawValue) {
			w.fallback("/wait", rawValue)
		}
	}

	// 10. Diagnosis.
	if rawValue, present := values["diagnosis"]; present {
		handled["diagnosis"] = true
		if !renderDiagnosisV4(w, rawValue) {
			w.fallback("/diagnosis", rawValue)
		}
	}

	// 11. Cleanup.
	if rawValue, present := values["cleanup"]; present {
		handled["cleanup"] = true
		if !renderCleanupV4(w, rawValue) {
			w.fallback("/cleanup", rawValue)
		}
	}

	// 12. Top-level error.
	if rawValue, present := values["error"]; present {
		handled["error"] = true
		if !renderErrorV4(w, "error", rawValue) {
			w.fallback("/error", rawValue)
		}
	}

	// 13. Recovery guidance.
	if rawValue, present := values["recovery"]; present {
		handled["recovery"] = true
		if !renderRecoveryV4(w, rawValue, values, requestRef) {
			w.fallback("/recovery", rawValue)
		}
	}

	// Unknown or unhandled top-level members keep every fact through json v1.
	for _, member := range members {
		if handled[member.name] {
			continue
		}
		w.fallback("/"+pointerPart(member.name), member.value)
	}

	w.line("end result")
	return w.String(), nil
}

func renderTextInputV4(raw []byte) (string, bool) {
	_, values, ok := resultMemberMap(raw)
	if !ok {
		return "", false
	}
	write, writeOK := rawStringValue(values["write"])
	submit, submitOK := rawStringValue(values["submit"])
	if !writeOK || !submitOK || !resultToken(write) || !resultToken(submit) {
		return "", false
	}
	for name := range values {
		if name != "write" && name != "submit" {
			return "", false
		}
	}
	return "text_input write=" + write + " submit=" + submit, true
}

func renderNotifyV4(w *resultV4Writer, values map[string]json.RawMessage, handled map[string]bool) {
	id := ""
	idPresent := false
	saved := ""
	savedPresent := false
	state := ""
	statePresent := false
	var notificationMembers []resultMember
	var notificationValues map[string]json.RawMessage
	var notificationRaw json.RawMessage
	if rawValue, present := values["message_id"]; present {
		handled["message_id"] = true
		if value, valid := rawStringValue(rawValue); valid && resultToken(value) {
			id, idPresent = value, true
		} else {
			w.fallback("/message_id", rawValue)
		}
	}
	if rawValue, present := values["saved"]; present {
		handled["saved"] = true
		if value, valid := rawBoolValue(rawValue); valid {
			saved, savedPresent = strconv.FormatBool(value), true
		} else {
			w.fallback("/saved", rawValue)
		}
	}
	if rawValue, present := values["notification"]; present {
		handled["notification"] = true
		notificationRaw = rawValue
		if !rawIsNull(rawValue) {
			notificationMembers, notificationValues, _ = resultMemberMap(rawValue)
			if value, valid := rawStringValue(notificationValues["state"]); valid && resultToken(value) {
				state, statePresent = value, true
			} else if _, exists := notificationValues["state"]; exists {
				w.fallback("/notification/state", notificationValues["state"])
			}
		}
	}
	line := "notify"
	if idPresent {
		line += " id=" + id
	}
	if savedPresent {
		line += " saved=" + saved
	}
	if statePresent {
		line += " state=" + state
	}
	if idPresent || savedPresent || statePresent {
		w.line(line)
	} else if notificationRaw != nil {
		if rawIsNull(notificationRaw) {
			w.fallback("/notification", notificationRaw)
			return
		}
		if len(notificationValues) == 0 {
			w.fallback("/notification", notificationRaw)
		}
	}
	if notificationValues == nil {
		return
	}
	if rawError, present := notificationValues["error"]; present {
		if !renderErrorV4(w, "notify.error", rawError) {
			w.fallback("/notification/error", rawError)
		}
	}
	for _, member := range notificationMembers {
		if member.name == "state" || member.name == "error" {
			continue
		}
		w.fallback("/notification/"+pointerPart(member.name), member.value)
	}
}

// renderImageV4 renders one ImageMeta object (top-level or observation image).
func renderImageV4(w *resultV4Writer, prefix string, raw []byte) bool {
	if rawIsNull(raw) {
		return false
	}
	members, values, ok := resultMemberMap(raw)
	if !ok {
		return false
	}
	parts := make([]string, 0, 8)
	width, widthOK := rawIntValue(values["width"])
	height, heightOK := rawIntValue(values["height"])
	if _, present := values["width"]; present {
		if !widthOK {
			return false
		}
	}
	if _, present := values["height"]; present {
		if !heightOK {
			return false
		}
	}
	if width > 0 && height > 0 {
		parts = append(parts, strconv.Itoa(width)+"x"+strconv.Itoa(height))
	} else if width != 0 || height != 0 {
		return false
	}
	if rawValue, present := values["read_only"]; present {
		value, valid := rawBoolValue(rawValue)
		if !valid {
			return false
		}
		if value {
			parts = append(parts, "read_only")
		}
	}
	if rawValue, present := values["media_type"]; present {
		value, valid := rawStringValue(rawValue)
		if !valid {
			return false
		}
		if value != "" && value != "image/jpeg" {
			if !resultToken(value) {
				return false
			}
			parts = append(parts, "media="+value)
		}
	}
	if rawScreen, present := values["screen"]; present {
		token, rotation, valid := compactScreenV4(rawScreen)
		if !valid {
			return false
		}
		if token != "" {
			parts = append(parts, "screen="+token)
		}
		if rotation != 0 {
			parts = append(parts, "rot="+strconv.Itoa(rotation))
		}
	}
	if rawValue, present := values["captured_at"]; present {
		value, valid := rawStringValue(rawValue)
		if !valid {
			return false
		}
		if value != "" {
			at, ok := resultV4Time(value)
			if !ok {
				return false
			}
			parts = append(parts, "at="+at)
		}
	}
	if len(parts) == 0 {
		if _, hasError := values["error"]; !hasError {
			return false
		}
		w.line(prefix)
	} else {
		w.line(prefix + " " + strings.Join(parts, " "))
	}
	if rawError, present := values["error"]; present {
		if !renderErrorV4(w, prefix+".error", rawError) {
			w.fallback(errorJSONPath(prefix+".error"), rawError)
		}
	}
	for _, member := range members {
		switch member.name {
		case "width", "height", "read_only", "media_type", "screen", "captured_at", "error", "base64":
			continue
		}
		w.fallback(imageJSONPath(prefix)+"/"+pointerPart(member.name), member.value)
	}
	return true
}

// compactScreenV4 returns WxH[@display_id] and the rotation of one Screen.
func compactScreenV4(raw []byte) (string, int, bool) {
	if rawIsNull(raw) {
		return "", 0, false
	}
	_, values, ok := resultMemberMap(raw)
	if !ok {
		return "", 0, false
	}
	width, widthOK := rawIntValue(values["width"])
	height, heightOK := rawIntValue(values["height"])
	rotation, rotationOK := rawIntValue(values["rotation"])
	if !widthOK || !heightOK || !rotationOK || width <= 0 || height <= 0 || rotation < 0 || rotation > 3 {
		return "", 0, false
	}
	for name := range values {
		switch name {
		case "width", "height", "rotation", "display_id":
		default:
			return "", 0, false
		}
	}
	token := strconv.Itoa(width) + "x" + strconv.Itoa(height)
	if rawDisplay, present := values["display_id"]; present && !rawIsNull(rawDisplay) {
		display, valid := rawIntValue(rawDisplay)
		if !valid {
			return "", 0, false
		}
		token += "@" + strconv.Itoa(display)
	}
	return token, rotation, true
}

func renderSessionV4(w *resultV4Writer, raw []byte) {
	if rawIsNull(raw) {
		w.line("session=null")
		return
	}
	members, values, ok := resultMemberMap(raw)
	if !ok {
		w.fallback("/session", raw)
		return
	}
	state, stateOK := rawStringValue(values["state"])
	mode, modeOK := rawStringValue(values["mode"])
	origin, originOK := rawStringValue(values["origin"])
	if !stateOK || !modeOK || !originOK || !resultToken(state) || !resultToken(mode) || !resultToken(origin) {
		w.fallback("/session", raw)
		return
	}
	line := "session " + state + " mode=" + mode + " origin=" + origin
	appendTime := func(name, label string) bool {
		rawValue, present := values[name]
		if !present {
			return true
		}
		value, valid := rawStringValue(rawValue)
		if !valid {
			return false
		}
		if value == "" {
			return true
		}
		formatted, ok := resultV4Time(value)
		if !ok {
			return false
		}
		line += " " + label + "=" + formatted
		return true
	}
	valid := appendTime("started_at", "started") &&
		appendTime("expires_at", "expires") &&
		appendTime("ended_at", "ended")
	if reasonRaw, present := values["reason"]; present {
		reason, ok := rawStringValue(reasonRaw)
		if !ok || (reason != "" && !resultToken(reason)) {
			valid = false
		} else if reason != "" {
			line += " reason=" + reason
		}
	}
	if !valid {
		w.fallback("/session", raw)
		return
	}
	w.line(line)
	for _, member := range members {
		switch member.name {
		case "state", "mode", "origin", "started_at", "expires_at", "ended_at", "reason", "agent":
			continue
		}
		w.fallback("/session/"+pointerPart(member.name), member.value)
	}
}

func renderObservationV4(w *resultV4Writer, raw []byte, top map[string]json.RawMessage) {
	if rawIsNull(raw) {
		w.fallback("/observation", raw)
		return
	}
	members, values, ok := resultMemberMap(raw)
	if !ok {
		w.fallback("/observation", raw)
		return
	}
	parts := make([]string, 0, 8)
	emitted := false

	id := ""
	if rawValue, present := values["id"]; present {
		if value, valid := rawStringValue(rawValue); valid && resultToken(value) {
			id = value
			parts = append(parts, value)
			emitted = true
		} else {
			w.fallback("/observation/id", rawValue)
			emitted = true
		}
	}
	statusComplete := false
	if rawValue, present := values["status"]; present {
		status, valid := rawStringValue(rawValue)
		if !valid {
			w.fallback("/observation/status", rawValue)
			emitted = true
		} else if status == "complete" {
			statusComplete = true
		} else if !resultToken(status) {
			w.fallback("/observation/status", rawValue)
			emitted = true
		} else {
			parts = append(parts, "status="+status)
			emitted = true
		}
	}
	if rawForeground, present := values["foreground"]; present {
		if token, valid := compactForegroundV4(rawForeground); valid {
			parts = append(parts, "fg="+token)
			emitted = true
		} else {
			w.fallback("/observation/foreground", rawForeground)
			emitted = true
		}
	}
	if rawValue, present := values["captured_at"]; present {
		value, valid := rawStringValue(rawValue)
		if valid && value != "" {
			if at, ok := resultV4Time(value); ok {
				parts = append(parts, "at="+at)
				emitted = true
			} else {
				w.fallback("/observation/captured_at", rawValue)
				emitted = true
			}
		} else if !valid {
			w.fallback("/observation/captured_at", rawValue)
			emitted = true
		}
	}
	// visual is optional: an absent member means this frame has no hint of this
	// kind. A value that is not a safe token falls back to the json v1 row.
	if rawValue, present := values["visual"]; present {
		value, valid := rawStringValue(rawValue)
		if valid && value != "" && resultToken(value) {
			parts = append(parts, "visual="+value)
			emitted = true
		} else {
			w.fallback("/observation/visual", rawValue)
			emitted = true
		}
	}
	hasSession := false
	if rawSession, present := top["session"]; present && !rawIsNull(rawSession) {
		hasSession = true
	}
	treeRaw, hasTree := values["tree"]
	if hasTree && rawIsNull(treeRaw) {
		hasTree = false
	}
	emitScreen := hasSession || !hasTree
	if rawScreen, present := values["screen"]; present {
		if rawIsNull(rawScreen) {
			w.fallback("/observation/screen", rawScreen)
			emitted = true
		} else if token, rotation, valid := compactScreenV4(rawScreen); valid {
			if emitScreen {
				parts = append(parts, "screen="+token)
				emitted = true
			}
			if rotation != 0 {
				parts = append(parts, "rot="+strconv.Itoa(rotation))
				emitted = true
			}
		} else {
			w.fallback("/observation/screen", rawScreen)
			emitted = true
		}
	}
	if rawMissing, present := values["missing"]; present {
		missing, valid := rawStringList(rawMissing)
		if !valid || rawIsNull(rawMissing) {
			w.fallback("/observation/missing", rawMissing)
			emitted = true
		} else if len(missing) > 0 {
			joined := strings.Join(missing, ",")
			if !resultToken(joined) {
				w.fallback("/observation/missing", rawMissing)
			} else {
				parts = append(parts, "missing="+joined)
			}
			emitted = true
		}
	}
	if len(parts) == 0 {
		if statusComplete {
			parts = append(parts, "status=complete")
			emitted = true
		} else if !emitted {
			w.fallback("/observation", raw)
			return
		}
	}
	if len(parts) > 0 {
		w.line("obs " + strings.Join(parts, " "))
	}
	if rawImage, present := values["image"]; present {
		if !renderImageV4(w, "obs.image", rawImage) {
			w.fallback("/observation/image", rawImage)
		}
	}
	if rawError, present := values["error"]; present {
		if !renderErrorV4(w, "obs.error", rawError) {
			w.fallback("/observation/error", rawError)
		}
	}
	for _, member := range members {
		switch member.name {
		case "id", "status", "foreground", "captured_at", "visual", "quality", "screen", "missing", "image", "error", "tree", "origin", "revision", "actionable_until":
			continue
		}
		w.fallback("/observation/"+pointerPart(member.name), member.value)
	}
	if hasTree {
		if block, ok := readableResultBlock("observation.tree", treeRaw, id); ok {
			w.block(block)
		} else {
			w.fallback("/observation/tree", treeRaw)
		}
	}
}

func compactForegroundV4(raw []byte) (string, bool) {
	if rawIsNull(raw) {
		return "", false
	}
	_, values, ok := resultMemberMap(raw)
	if !ok {
		return "", false
	}
	for name := range values {
		switch name {
		case "state", "package", "task_id", "reason":
		default:
			return "", false
		}
	}
	state, valid := rawStringValue(values["state"])
	if !valid {
		return "", false
	}
	switch state {
	case "present":
		pkg := ""
		if rawValue, present := values["package"]; present {
			value, ok := rawStringValue(rawValue)
			if !ok || (value != "" && (!resultToken(value) || strings.ContainsAny(value, "()"))) {
				return "", false
			}
			pkg = value
		}
		task := ""
		if rawValue, present := values["task_id"]; present && !rawIsNull(rawValue) {
			value, ok := rawIntValue(rawValue)
			if !ok {
				return "", false
			}
			task = strconv.Itoa(value)
		}
		switch {
		case pkg != "" && task != "":
			return pkg + "#" + task, true
		case pkg != "":
			return pkg, true
		case task != "":
			return "#" + task, true
		default:
			return "present", true
		}
	case "none":
		return "none", true
	case "unknown":
		if rawValue, present := values["reason"]; present {
			reason, ok := rawStringValue(rawValue)
			if !ok {
				return "", false
			}
			if reason == "" {
				return "unknown", true
			}
			if !resultToken(reason) || strings.ContainsAny(reason, "()") {
				return "", false
			}
			return "unknown(" + reason + ")", true
		}
		return "unknown", true
	default:
		return "", false
	}
}

// resultPointer converts a dotted result path to an RFC6901 pointer.
func resultPointer(path string) string {
	parts := strings.Split(path, ".")
	for index := range parts {
		parts[index] = pointerPart(parts[index])
	}
	return "/" + strings.Join(parts, "/")
}

// imageJSONPath maps an image rendering prefix to its real JSON pointer.
func imageJSONPath(prefix string) string {
	if prefix == "obs.image" {
		return "/observation/image"
	}
	return "/" + pointerPart(prefix)
}

// errorJSONPath maps an error rendering prefix to its real JSON pointer.
func errorJSONPath(prefix string) string {
	switch prefix {
	case "error":
		return "/error"
	case "obs.error":
		return "/observation/error"
	case "obs.image.error":
		return "/observation/image/error"
	case "image.error":
		return "/image/error"
	case "notify.error":
		return "/notification/error"
	case "cleanup.error":
		return "/cleanup/error"
	}
	return resultPointer(prefix)
}

// renderAppV4 renders the kind-discriminated app object and its collection
// blocks. It returns false only when the object cannot be classified at all;
// individual unknown members still fall back in place.
func renderAppV4(w *resultV4Writer, raw []byte) bool {
	members, values, ok := resultMemberMap(raw)
	if !ok {
		return false
	}
	kind, kindOK := rawStringValue(values["kind"])
	if !kindOK || !resultToken(kind) {
		return false
	}
	line := "app " + kind
	appendToken := func(name, label string) bool {
		rawValue, present := values[name]
		if !present {
			return true
		}
		value, valid := rawStringValue(rawValue)
		if !valid || (value != "" && !resultToken(value)) {
			return false
		}
		if value != "" {
			line += " " + label + "=" + value
		}
		return true
	}
	appendInt := func(name, label string) bool {
		rawValue, present := values[name]
		if !present {
			return true
		}
		if rawIsNull(rawValue) {
			return false
		}
		value, valid := rawIntValue(rawValue)
		if !valid {
			return false
		}
		line += " " + label + "=" + strconv.Itoa(value)
		return true
	}
	appendBool := func(name, label string) bool {
		rawValue, present := values[name]
		if !present {
			return true
		}
		value, valid := rawBoolValue(rawValue)
		if !valid {
			return false
		}
		line += " " + label + "=" + strconv.FormatBool(value)
		return true
	}
	appendNullableBool := func(name, label string) bool {
		rawValue, present := values[name]
		if !present {
			return true
		}
		if rawIsNull(rawValue) {
			line += " " + label + "=null"
			return true
		}
		value, valid := rawBoolValue(rawValue)
		if !valid {
			return false
		}
		line += " " + label + "=" + strconv.FormatBool(value)
		return true
	}
	appendIntArray := func(name, label string) bool {
		rawValue, present := values[name]
		if !present {
			return true
		}
		if rawIsNull(rawValue) {
			return false
		}
		if _, valid := rawIntList(rawValue); !valid {
			return false
		}
		line += " " + label + "=" + compactResultJSON(rawValue)
		return true
	}
	valid := appendToken("package", "pkg") &&
		appendInt("task_id", "task") &&
		appendInt("source_task_id", "src_task") &&
		appendInt("source_display_id", "src_display") &&
		appendInt("display_id", "display") &&
		appendToken("ownership", "ownership")
	if valid {
		valid = appendBool("reused", "reused") &&
			appendNullableBool("state_preserved", "state_preserved") &&
			appendToken("foreground_restore", "fg_restore") &&
			appendBool("complete", "complete") &&
			appendIntArray("target_pids", "target_pids") &&
			appendIntArray("killed_pids", "killed_pids") &&
			appendIntArray("remaining_pids", "remaining_pids") &&
			appendNullableBool("restarted", "restarted") &&
			appendInt("total", "total") &&
			appendBool("truncated", "truncated")
	}
	if !valid {
		return false
	}
	if rawForeground, present := values["foreground"]; present {
		token, valid := compactForegroundV4(rawForeground)
		if !valid {
			return false
		}
		line += " fg=" + token
	}
	if rawValue, present := values["captured_at"]; present {
		value, valid := rawStringValue(rawValue)
		if !valid {
			return false
		}
		if value != "" {
			at, ok := resultV4Time(value)
			if !ok {
				return false
			}
			line += " at=" + at
		}
	}
	w.line(line)
	for _, member := range members {
		switch member.name {
		case "kind", "items", "total", "truncated", "captured_at", "display_id",
			"foreground", "package", "task_id", "ownership", "reused",
			"state_preserved", "source_display_id", "foreground_restore",
			"tasks", "complete", "source_task_id", "target_pids", "killed_pids",
			"remaining_pids", "restarted":
			continue
		}
		w.fallback(resultPointer("app")+"/"+pointerPart(member.name), member.value)
	}
	if rawItems, present := values["items"]; present {
		if !renderAppItemsV4(w, kind, rawItems) {
			w.fallback("/app/items", rawItems)
		}
	}
	if rawTasks, present := values["tasks"]; present {
		if !renderAppTasksV4(w, rawTasks) {
			w.fallback("/app/tasks", rawTasks)
		}
	}
	return true
}

func renderAppItemsV4(w *resultV4Writer, kind string, raw []byte) bool {
	if rawIsNull(raw) || len(raw) == 0 || raw[0] != '[' {
		return false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return false
	}
	var body strings.Builder
	fmt.Fprintf(&body, "app_items v1 kind=%s count=%d\n", kind, len(items))
	switch kind {
	case "find":
		for index, itemRaw := range items {
			itemMembers, item, ok := resultMemberMap(itemRaw)
			if !ok {
				fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/items/%d", index)), compactResultJSON(itemRaw))
				continue
			}
			name, nameOK := rawStringValue(item["name"])
			pkg, pkgOK := rawStringValue(item["package"])
			if !nameOK || !pkgOK || !resultToken(pkg) {
				fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/items/%d", index)), compactResultJSON(itemRaw))
				continue
			}
			for _, member := range itemMembers {
				if member.name != "name" && member.name != "package" {
					fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/items/%d/%s", index, pointerPart(member.name))), compactResultJSON(member.value))
				}
			}
			fmt.Fprintf(&body, "%s %s\n", pkg, quoteResult(name))
		}
	case "list":
		for index, itemRaw := range items {
			itemMembers, item, ok := resultMemberMap(itemRaw)
			if !ok {
				fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/items/%d", index)), compactResultJSON(itemRaw))
				continue
			}
			taskID, taskOK := rawIntValue(item["task_id"])
			pkg, pkgOK := rawStringValue(item["package"])
			ownership, ownershipOK := rawStringValue(item["ownership"])
			if !taskOK || !pkgOK || !ownershipOK || !resultToken(pkg) || !resultToken(ownership) {
				fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/items/%d", index)), compactResultJSON(itemRaw))
				continue
			}
			itemLine := fmt.Sprintf("#%d %s %s", taskID, pkg, ownership)
			if rawDisplay, present := item["origin_display"]; present && !rawIsNull(rawDisplay) {
				display, displayOK := rawIntValue(rawDisplay)
				if !displayOK {
					fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/items/%d", index)), compactResultJSON(itemRaw))
					continue
				}
				itemLine += " origin_display=" + strconv.Itoa(display)
			}
			for _, member := range itemMembers {
				switch member.name {
				case "task_id", "package", "ownership", "origin_display":
				default:
					fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/items/%d/%s", index, pointerPart(member.name))), compactResultJSON(member.value))
				}
			}
			body.WriteString(itemLine + "\n")
		}
	default:
		return false
	}
	body.WriteString("end app_items\n")
	w.block(body.String())
	return true
}

func renderAppTasksV4(w *resultV4Writer, raw []byte) bool {
	if rawIsNull(raw) || len(raw) == 0 || raw[0] != '[' {
		return false
	}
	var tasks []json.RawMessage
	if json.Unmarshal(raw, &tasks) != nil {
		return false
	}
	var body strings.Builder
	fmt.Fprintf(&body, "app_tasks v1 count=%d\n", len(tasks))
	for index, taskRaw := range tasks {
		taskMembers, task, ok := resultMemberMap(taskRaw)
		if !ok {
			fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/tasks/%d", index)), compactResultJSON(taskRaw))
			continue
		}
		taskID, taskOK := rawIntValue(task["task_id"])
		outcome, outcomeOK := rawStringValue(task["outcome"])
		if !taskOK || !outcomeOK || !resultToken(outcome) {
			fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/tasks/%d", index)), compactResultJSON(taskRaw))
			continue
		}
		line := fmt.Sprintf("#%d %s", taskID, outcome)
		if rawValue, present := task["recents_cleaned"]; present {
			if rawIsNull(rawValue) {
				line += " recents_cleaned=null"
			} else if value, valid := rawBoolValue(rawValue); valid {
				line += " recents_cleaned=" + strconv.FormatBool(value)
			} else {
				fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/tasks/%d/recents_cleaned", index)), compactResultJSON(rawValue))
			}
		}
		if rawError, present := task["error"]; present && !rawIsNull(rawError) {
			_, errorValues, errorOK := resultMemberMap(rawError)
			code, codeOK := rawStringValue(errorValues["code"])
			message, messageOK := rawStringValue(errorValues["message"])
			if !errorOK || !codeOK || !messageOK || !resultToken(code) {
				fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/tasks/%d/error", index)), compactResultJSON(rawError))
			} else {
				line += " error=" + code + " " + quoteResult(message)
				if rawDetails, present := errorValues["details"]; present && !rawIsNull(rawDetails) {
					line += " details=" + compactResultJSON(rawDetails)
				}
			}
		}
		for _, member := range taskMembers {
			switch member.name {
			case "task_id", "outcome", "recents_cleaned", "error":
			default:
				fmt.Fprintf(&body, "json v1 path=%s value=%s\n", quoteResult(fmt.Sprintf("/app/tasks/%d/%s", index, pointerPart(member.name))), compactResultJSON(member.value))
			}
		}
		body.WriteString(line + "\n")
	}
	body.WriteString("end app_tasks\n")
	w.block(body.String())
	return true
}

func renderWaitV4(w *resultV4Writer, raw []byte) bool {
	_, values, ok := resultMemberMap(raw)
	if !ok {
		return false
	}
	trigger, triggerOK := rawStringValue(values["trigger"])
	elapsed, elapsedOK := rawIntValue(values["elapsed_ms"])
	if !triggerOK || !elapsedOK || !resultToken(trigger) {
		return false
	}
	line := "wait trigger=" + trigger
	if rawMatched, present := values["matched"]; present {
		matched, valid := rawStringList(rawMatched)
		if !valid || rawIsNull(rawMatched) {
			return false
		}
		if len(matched) > 0 {
			joined := strings.Join(matched, ",")
			if !resultToken(joined) {
				return false
			}
			line += " matched=" + joined
		}
	}
	line += " elapsed=" + strconv.Itoa(elapsed) + "ms"
	for name := range values {
		switch name {
		case "trigger", "matched", "elapsed_ms":
		default:
			return false
		}
	}
	w.line(line)
	return true
}

func renderDiagnosisV4(w *resultV4Writer, raw []byte) bool {
	members, values, ok := resultMemberMap(raw)
	if !ok {
		return false
	}
	summary, summaryOK := rawStringValue(values["summary"])
	coverage, coverageOK := rawStringValue(values["coverage"])
	truncated, truncatedOK := rawBoolValue(values["truncated"])
	if !summaryOK || !coverageOK || !truncatedOK || !resultToken(coverage) {
		return false
	}
	line := "diagnosis summary=" + quoteResult(summary) + " coverage=" + coverage + " truncated=" + strconv.FormatBool(truncated)
	if rawValue, present := values["operation_id"]; present {
		value, valid := rawStringValue(rawValue)
		if !valid || !resultToken(value) {
			return false
		}
		line += " op=" + value
	}
	if rawValue, present := values["outcome"]; present {
		value, valid := rawStringValue(rawValue)
		if !valid || !resultToken(value) {
			return false
		}
		line += " outcome=" + value
	}
	w.line(line)
	if rawValue, present := values["issues"]; present {
		if rawIsNull(rawValue) {
			w.fallback("/diagnosis/issues", rawValue)
		} else {
			w.line("diagnosis.issues=" + compactResultJSON(rawValue))
		}
	}
	if rawValue, present := values["timing_ms"]; present {
		if rawIsNull(rawValue) {
			w.fallback("/diagnosis/timing_ms", rawValue)
		} else {
			w.line("diagnosis.timing_ms=" + compactResultJSON(rawValue))
		}
	}
	for _, member := range members {
		switch member.name {
		case "summary", "issues", "timing_ms", "timeline", "coverage", "truncated", "operation_id", "outcome":
			continue
		}
		w.fallback("/diagnosis/"+pointerPart(member.name), member.value)
	}
	if rawTimeline, present := values["timeline"]; present {
		if rawIsNull(rawTimeline) {
			w.fallback("/diagnosis/timeline", rawTimeline)
		} else if events, valid := rawArrayValue(rawTimeline); valid && len(events) == 0 {
			// An empty timeline carries no event block; the structured JSON is
			// the only place an explicit empty list matters.
		} else if block, ok := readableResultBlock("diagnosis.timeline", rawTimeline, ""); ok {
			w.block(block)
		} else {
			w.fallback("/diagnosis/timeline", rawTimeline)
		}
	}
	return true
}

func renderCleanupV4(w *resultV4Writer, raw []byte) bool {
	members, values, ok := resultMemberMap(raw)
	if !ok {
		return false
	}
	state, stateOK := rawStringValue(values["state"])
	if !stateOK || !resultToken(state) {
		return false
	}
	line := "cleanup " + state
	for _, field := range []struct{ name, label string }{
		{"execution", "exec"}, {"tasks", "tasks"}, {"recents", "recents"}, {"display", "display"},
	} {
		rawValue, present := values[field.name]
		if !present {
			continue
		}
		value, valid := rawStringValue(rawValue)
		if !valid || !resultToken(value) {
			return false
		}
		line += " " + field.label + "=" + value
	}
	w.line(line)
	if rawError, present := values["error"]; present {
		if !renderErrorV4(w, "cleanup.error", rawError) {
			w.fallback("/cleanup/error", rawError)
		}
	}
	for _, member := range members {
		switch member.name {
		case "state", "execution", "tasks", "recents", "display", "error":
			continue
		}
		w.fallback("/cleanup/"+pointerPart(member.name), member.value)
	}
	return true
}

// renderErrorV4 writes prefix <code> "<message>" plus optional details.
func renderErrorV4(w *resultV4Writer, prefix string, raw []byte) bool {
	if rawIsNull(raw) {
		return false
	}
	members, values, ok := resultMemberMap(raw)
	if !ok {
		return false
	}
	code, codeOK := rawStringValue(values["code"])
	message, messageOK := rawStringValue(values["message"])
	if !codeOK || !messageOK || !resultToken(code) {
		return false
	}
	w.line(prefix + " " + code + " " + quoteResult(message))
	if rawDetails, present := values["details"]; present {
		if rawIsNull(rawDetails) {
			w.fallback(errorJSONPath(prefix)+"/details", rawDetails)
		} else {
			w.line(prefix + ".details=" + compactResultJSON(rawDetails))
		}
	}
	for _, member := range members {
		switch member.name {
		case "code", "message", "details":
			continue
		}
		w.fallback(errorJSONPath(prefix)+"/"+pointerPart(member.name), member.value)
	}
	return true
}

func renderRecoveryV4(w *resultV4Writer, raw []byte, top map[string]json.RawMessage, requestRef string) bool {
	_, values, ok := resultMemberMap(raw)
	if !ok {
		return false
	}
	entry, entryOK := rawStringValue(values["entry"])
	reference, referenceOK := rawStringValue(values["reference"])
	if !entryOK || !referenceOK || !resultToken(entry) || !resultToken(reference) {
		return false
	}
	resolved := resolveRecoveryReference(top, requestRef)
	if !resultToken(resolved) {
		return false
	}
	line := "recovery entry=" + entry + " ref=" + resolved
	if rawValue, present := values["input_state"]; present {
		value, valid := rawStringValue(rawValue)
		if !valid || !resultToken(value) {
			return false
		}
		line += " input=" + value
	}
	if rawValue, present := values["cause"]; present {
		value, valid := rawStringValue(rawValue)
		if !valid || !resultToken(value) {
			return false
		}
		line += " cause=" + value
	}
	for name := range values {
		switch name {
		case "entry", "reference", "input_state", "cause":
		default:
			return false
		}
	}
	w.line(line)
	return true
}

// isObsID reports the public observation identifier form used by exact
// recovery resolution: a safe token beginning with O.
func isObsID(value string) bool {
	return len(value) >= 2 && value[0] == 'O' && resultToken(value)
}

// isOpID reports the public operation identifier form used by exact recovery
// resolution: a safe token beginning with P.
func isOpID(value string) bool {
	return len(value) >= 2 && value[0] == 'P' && resultToken(value)
}

// resultErrCode prefers the top-level error code, then the observation error
// code produced by capture-failure paths (observe/wait_for).
func resultErrCode(top map[string]json.RawMessage) string {
	if rawError, present := top["error"]; present {
		if _, values, ok := resultMemberMap(rawError); ok {
			if code, valid := rawStringValue(values["code"]); valid {
				return code
			}
		}
	}
	if rawObservation, present := top["observation"]; present {
		if _, observation, ok := resultMemberMap(rawObservation); ok {
			if rawError, present := observation["error"]; present {
				if _, values, ok := resultMemberMap(rawError); ok {
					if code, valid := rawStringValue(values["code"]); valid {
						return code
					}
				}
			}
		}
	}
	return ""
}

func resultTopString(top map[string]json.RawMessage, name string) string {
	if raw, present := top[name]; present {
		if value, ok := rawStringValue(raw); ok {
			return value
		}
	}
	return ""
}

func resultTopBoolPtr(top map[string]json.RawMessage, name string) *bool {
	raw, present := top[name]
	if !present || rawIsNull(raw) {
		return nil
	}
	if value, ok := rawBoolValue(raw); ok {
		return &value
	}
	return nil
}

func resultNestedString(top map[string]json.RawMessage, object, name string) string {
	raw, present := top[object]
	if !present {
		return ""
	}
	_, values, ok := resultMemberMap(raw)
	if !ok {
		return ""
	}
	return resultTopString(values, name)
}

func resultRecoveryFields(top map[string]json.RawMessage) (entry, reference, cause string) {
	raw, present := top["recovery"]
	if !present {
		return "", "", ""
	}
	_, values, ok := resultMemberMap(raw)
	if !ok {
		return "", "", ""
	}
	entry, _ = rawStringValue(values["entry"])
	reference, _ = rawStringValue(values["reference"])
	cause, _ = rawStringValue(values["cause"])
	return entry, reference, cause
}

func resultCleanupState(top map[string]json.RawMessage) string {
	raw, present := top["cleanup"]
	if !present {
		return ""
	}
	_, values, ok := resultMemberMap(raw)
	if !ok {
		return ""
	}
	state, _ := rawStringValue(values["state"])
	return state
}

func resultErrorDetailsStartOperation(top map[string]json.RawMessage) string {
	rawError, present := top["error"]
	if !present {
		return ""
	}
	_, values, ok := resultMemberMap(rawError)
	if !ok {
		return ""
	}
	rawDetails, present := values["details"]
	if !present || rawIsNull(rawDetails) {
		return ""
	}
	_, details, ok := resultMemberMap(rawDetails)
	if !ok {
		return ""
	}
	value, _ := rawStringValue(details["start_operation_id"])
	return value
}

// resolveRecoveryReference implements proposal §4.3 route R1 exactly: it never
// invents a reference and falls back to the abstract O/P words at every edge
// where safety cannot be proven from this single call's facts.
func resolveRecoveryReference(top map[string]json.RawMessage, requestRef string) string {
	entry, reference, cause := resultRecoveryFields(top)
	if reference == "none" {
		return "none"
	}
	switch {
	case entry == "retry" && reference == "O":
		if observationID := resultNestedString(top, "observation", "id"); isObsID(observationID) {
			return observationID
		}
		if isObsID(requestRef) {
			return requestRef
		}
		return "O"
	case (entry == "status" || entry == "end") && reference == "O":
		if target := resultTopString(top, "target_reference"); isObsID(target) {
			return target
		}
		if isObsID(requestRef) {
			return requestRef
		}
		return "O"
	case entry == "status" && reference == "P":
		return resolveStatusRecoveryP(top, requestRef, cause)
	case entry == "observe" && reference == "P":
		return resolveObserveRecoveryP(top, requestRef, cause)
	case entry == "end" && reference == "P":
		return resolveEndRecoveryP(top, requestRef)
	default:
		return reference
	}
}

func resolveStatusRecoveryP(top map[string]json.RawMessage, requestRef, cause string) string {
	errCode := resultErrCode(top)
	operationID := resultTopString(top, "operation_id")
	blocking := resultTopString(top, "blocking_operation_id")
	target := resultTopString(top, "target_reference")

	// Red line 1: a cross-Agent device_busy without an explicit blocking
	// operation must never point at this adapter's own empty operation.
	if errCode == "device_busy" {
		if isOpID(blocking) {
			return blocking
		}
		return "P"
	}
	// Query/reference target anomalies: prefer the fact the caller asked about.
	if errCode == "reference_has_no_session" || errCode == "operation_unknown" || errCode == "operation_not_found" ||
		(errCode == "request_closed" && resultTopBoolPtr(top, "request_closed") == nil) {
		if isOpID(target) {
			return target
		}
		if isOpID(requestRef) {
			return requestRef
		}
		return "P"
	}
	// The current unresolved/revoked/closed/pending operation outranks a
	// possibly stale session.origin.
	if isOpID(operationID) {
		requestClosed := resultTopBoolPtr(top, "request_closed")
		cleanupState := resultCleanupState(top)
		if resultTopString(top, "outcome") == "uncertain" ||
			resultTopString(top, "outcome") == "partial" ||
			resultTopString(top, "operation_state") == "running" ||
			resultTopString(top, "phase") == "executed_revoked" ||
			(requestClosed != nil && *requestClosed) ||
			recoveryCauseIs(cause, "running", "executed_revoked", "cleanup_pending") ||
			errorCodeIs(errCode, "call_cancelled", "transport_unavailable", "invalid_response", "task_lifecycle_busy",
				"text_submit_not_executed") ||
			cleanupState == "pending" || cleanupState == "unconfirmed" {
			return operationID
		}
	}
	if start := resultTopString(top, "start_operation_id"); isOpID(start) {
		return start
	}
	if origin := resultNestedString(top, "session", "origin"); isOpID(origin) {
		return origin
	}
	if isOpID(target) {
		return target
	}
	// Red line 4: an admission-rejected start (recovering, device_unavailable,
	// ledger_unavailable, ...) must not return the rejected start's own op.
	return "P"
}

func resolveObserveRecoveryP(top map[string]json.RawMessage, requestRef, cause string) string {
	errCode := resultErrCode(top)
	operationID := resultTopString(top, "operation_id")
	// A confirmed partial text act owns the observe recovery even when a frozen
	// copy still carries session.origin: the durable observe(P, retired) must
	// resolve to this operation's own P both when the first response published
	// a fresh ready O and when that observation failed or was never attached.
	if (resultTopString(top, "outcome") == "partial" || errCode == "text_submit_not_executed") &&
		isOpID(operationID) {
		return operationID
	}
	if origin := resultNestedString(top, "session", "origin"); isOpID(origin) {
		return origin
	}
	if isOpID(requestRef) &&
		errCode != "operation_unknown" && errCode != "operation_not_found" &&
		errCode != "request_closed" && errCode != "reference_has_no_session" {
		return requestRef
	}
	if isOpID(operationID) {
		if cause == "post_observation_failed" ||
			resultTopString(top, "phase") == "post_observation_failed" ||
			resultTopString(top, "outcome") == "uncertain" ||
			resultTopString(top, "outcome") == "partial" ||
			errorCodeIs(errCode,
				"observation_expired", "observation_claimed", "observation_retired",
				"hierarchy_unavailable", "stale_screen", "tree_too_large",
				"control_ui_on_virtual_display", "display_unavailable",
				"text_submit_not_executed") {
			return operationID
		}
	}
	// Red line 2: observation_not_found / no_ready_observation /
	// content_unavailable must never point at the failed operation itself.
	return "P"
}

func resolveEndRecoveryP(top map[string]json.RawMessage, requestRef string) string {
	if start := resultErrorDetailsStartOperation(top); isOpID(start) {
		return start
	}
	if origin := resultNestedString(top, "session", "origin"); isOpID(origin) {
		return origin
	}
	if start := resultTopString(top, "start_operation_id"); isOpID(start) {
		return start
	}
	if isOpID(requestRef) {
		return requestRef
	}
	// Red line 3: a session_ended/session_expired/control_revoked end without a
	// session must never return this call's own op; that could bind to a new
	// active session at admission.
	return "P"
}

func recoveryCauseIs(cause string, values ...string) bool {
	for _, value := range values {
		if cause == value {
			return true
		}
	}
	return false
}

func errorCodeIs(code string, values ...string) bool {
	for _, value := range values {
		if code == value {
			return true
		}
	}
	return false
}

// The helpers below back the typed block renderers shared by result v4 and the
// legacy diagnostic v1 whitelist.

// Before using a typed renderer, require its wire round trip to retain every
// member and presence distinction. Future/unrepresentable composites go whole
// into json v1, including unknown nested Node/SetNode/InspectionMatch fields.
func sameResultJSON(a, b []byte) bool {
	x, y := jsontext.Value(bytes.Clone(a)), jsontext.Value(bytes.Clone(b))
	// JCS normally rounds numbers to float64. Disable numeric rewriting: the
	// guard must distinguish all protocol integers, including values above 2^53.
	options := []jsontext.Options{jsontext.CanonicalizeRawInts(false), jsontext.CanonicalizeRawFloats(false)}
	return x.Canonicalize(options...) == nil && y.Canonicalize(options...) == nil && bytes.Equal(x, y)
}
func losslessTypedResult(raw []byte, target any) bool {
	if jsonv2.Unmarshal(raw, target) != nil {
		return false
	}
	b, err := jsonv2.Marshal(target, jsonv2.Deterministic(true))
	return err == nil && sameResultJSON(raw, b)
}

func readableResultBlock(key string, raw []byte, observationID string) (string, bool) {
	switch key {
	case "observation.tree":
		var tree protocol.ReadTreeValue
		if !validateV4ObservationToken(observationID) || !losslessTypedResult(raw, &tree) {
			return "", false
		}
		return renderTreeText(&protocol.Result{Observation: &protocol.Observation{ID: observationID, Tree: &tree}})
	case "inspection":
		var inspection protocol.InspectionView
		if !losslessTypedResult(raw, &inspection) {
			return "", false
		}
		return renderInspectionText(&inspection)
	case "shell":
		return readableShell(raw)
	case "diagnosis.timeline":
		var events []json.RawMessage
		if jsonv2.Unmarshal(raw, &events) != nil || len(events) == 0 {
			return "", false
		}
		var b strings.Builder
		fmt.Fprintf(&b, "timeline v1 events=%d\n", len(events))
		for _, e := range events {
			if len(e) == 0 || e[0] != '{' {
				return "", false
			}
			b.WriteString("event " + compactResultJSON(e) + "\n")
		}
		b.WriteString("end timeline\n")
		return b.String(), true
	}
	return "", false
}

func rawMemberMap(raw []byte) ([]resultMember, map[string]json.RawMessage, bool) {
	return resultMemberMap(raw)
}

func readableShell(raw []byte) (string, bool) {
	members, m, ok := rawMemberMap(raw)
	if !ok {
		return "", false
	}
	for _, key := range []string{"termination", "exit_code", "output", "truncated"} {
		if _, ok := m[key]; !ok {
			return "", false
		}
	}
	var termination string
	if json.Unmarshal(m["termination"], &termination) != nil || !resultToken(termination) {
		return "", false
	}
	for _, key := range []string{"exit_code", "truncated"} {
		if !resultToken(string(m[key])) {
			return "", false
		}
	}
	var b strings.Builder
	outputBytes := "null"
	var output string
	if string(m["output"]) != "null" {
		if json.Unmarshal(m["output"], &output) != nil {
			return "", false
		}
		outputBytes = strconv.Itoa(len(output))
	}
	fmt.Fprintf(&b, "shell v1 termination=%s exit_code=%s truncated=%s output_bytes=%s\n", termination, m["exit_code"], m["truncated"], outputBytes)
	if outputBytes != "null" {
		if output == "" {
			b.WriteString("output \"\"\n")
		}
		for len(output) > 0 {
			chunk, rest, found := strings.Cut(output, "\n")
			if found {
				chunk += "\n"
			}
			b.WriteString("output " + quoteResult(chunk) + "\n")
			output = rest
		}
	}
	if v, ok := m["output_error"]; ok {
		b.WriteString("output_error " + compactResultJSON(v) + "\n")
	}
	b.WriteString("end shell\n")
	for _, v := range members {
		switch v.name {
		case "termination", "exit_code", "output", "truncated", "output_error":
		default:
			fmt.Fprintf(&b, "json v1 path=%s value=%s\n", quoteResult("/shell/"+pointerPart(v.name)), compactResultJSON(v.value))
		}
	}
	return b.String(), true
}

func encodeResultDiagnostic(raw []byte) (string, error) {
	members, err := resultMembers(raw)
	if err != nil {
		return "", err
	}
	w := &resultTextWriter{}
	w.line("diagnostic v1 is_error=true")
	for _, m := range members {
		switch m.name {
		case "inspection", "shell", "notification", "diagnosis":
			continue
		case "app":
			// Only app.kind is whitelisted into diagnostic v1; the rest of the
			// app object (items/tasks/PIDs/...) is not a diagnostic fact.
			parts, err := resultMembers(m.value)
			if err != nil {
				continue
			}
			for _, v := range parts {
				if v.name == "kind" {
					w.member("app", v.name, "/app/kind", v.value)
				}
			}
		case "observation":
			parts, err := resultMembers(m.value)
			if err != nil {
				continue
			}
			for _, v := range parts {
				if v.name == "status" || v.name == "id" {
					w.member("observation", v.name, "/observation/"+v.name, v.value)
				}
			}
		case "wait":
			// A failed verification acquisition still knows which wait branch
			// stopped waiting and how long it took; a match list would claim a
			// frame that was never published, so it is never carried here.
			parts, err := resultMembers(m.value)
			if err != nil {
				continue
			}
			for _, v := range parts {
				if v.name == "trigger" || v.name == "elapsed_ms" {
					w.member("wait", v.name, "/wait/"+v.name, v.value)
				}
			}
		default:
			w.member("", m.name, "/"+pointerPart(m.name), m.value)
		}
	}
	w.line("end diagnostic")
	return w.String(), nil
}

// resultTextWriter and the legacy v3 classification are retained only for
// diagnostic v1 and for the v3 decoder used by historical fixtures.
type resultFieldKind uint8

const (
	fieldToken resultFieldKind = iota + 1
	fieldString
	fieldJSON
	fieldObject
	fieldBlock
	fieldLiteral
)

var resultFields = func() map[string]resultFieldKind {
	m := map[string]resultFieldKind{}
	add := func(prefix, names string, kind resultFieldKind) {
		for _, name := range strings.Fields(names) {
			m[prefix+name] = kind
		}
	}
	add("", "operation_id operation_state outcome phase target_reference request_closed blocking_operation_id can_start blocking_code start_operation_id observation_state input_state message_id saved", fieldToken)
	add("", "effect", fieldObject)
	add("effect.", "state", fieldToken)
	add("", "text_input", fieldObject)
	add("text_input.", "write submit", fieldToken)
	add("", "session cleanup error recovery observation app notification diagnosis", fieldObject)
	add("", "image", fieldJSON)
	add("", "inspection shell", fieldBlock)
	add("", "wait", fieldObject)
	add("wait.", "trigger", fieldToken)
	add("wait.", "matched", fieldJSON)
	add("wait.", "elapsed_ms", fieldLiteral)
	add("session.", "origin state mode started_at expires_at ended_at reason", fieldToken)
	add("session.", "agent", fieldString)
	add("cleanup.", "state execution tasks recents display", fieldToken)
	add("cleanup.", "error", fieldJSON)
	add("error.", "code", fieldToken)
	add("error.", "message", fieldString)
	add("error.", "details", fieldJSON)
	add("recovery.", "entry reference input_state cause", fieldToken)
	add("observation.", "status id origin revision captured_at actionable_until visual", fieldToken)
	add("observation.", "quality", fieldObject)
	add("observation.quality.", "state stop_reason", fieldToken)
	add("observation.quality.", "samples", fieldLiteral)
	add("observation.", "screen foreground missing image error", fieldJSON)
	add("observation.", "tree", fieldBlock)
	add("inspection.", "origin", fieldToken)
	add("app.", "kind ownership foreground_restore captured_at", fieldToken)
	add("app.", "package", fieldString)
	add("app.", "task_id source_task_id display_id source_display_id total", fieldLiteral)
	add("app.", "reused state_preserved complete truncated restarted", fieldLiteral)
	add("app.", "items tasks foreground target_pids killed_pids remaining_pids", fieldJSON)
	add("notification.", "state", fieldToken)
	add("notification.", "error", fieldJSON)
	add("diagnosis.", "summary", fieldString)
	add("diagnosis.", "issues timing_ms", fieldJSON)
	add("diagnosis.", "coverage truncated operation_id outcome", fieldToken)
	add("diagnosis.", "timeline", fieldBlock)
	add("", "request_closed can_start saved", fieldLiteral)
	add("observation.", "revision", fieldLiteral)
	add("diagnosis.", "truncated", fieldLiteral)
	return m
}()

type resultTextWriter struct {
	strings.Builder
	groupObservation     bool
	observationGroupOpen bool
	observationID        string
}

func (w *resultTextWriter) line(s string) {
	w.closeObservationGroup()
	w.WriteString(s)
	w.WriteByte('\n')
}
func (w *resultTextWriter) field(key, value string) {
	if w.groupObservation {
		if relative, ok := strings.CutPrefix(key, "observation."); ok {
			if !w.observationGroupOpen {
				w.WriteString("observation\n")
				w.observationGroupOpen = true
			}
			w.WriteByte(' ')
			w.WriteString(relative)
			w.WriteByte('=')
			w.WriteString(value)
			w.WriteByte('\n')
			return
		}
	}
	w.line(key + "=" + value)
}
func (w *resultTextWriter) closeObservationGroup() {
	w.observationGroupOpen = false
}
func (w *resultTextWriter) block(s string) {
	w.closeObservationGroup()
	w.WriteByte('\n')
	w.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		w.WriteByte('\n')
	}
}
func (w *resultTextWriter) fallback(path string, raw []byte) {
	w.line("json v1 path=" + quoteResult(path) + " value=" + compactResultJSON(raw))
}

func (w *resultTextWriter) member(parentKey, name, path string, raw []byte) {
	// Dots join structural levels in resultFields. A literal member whose name
	// contains one is therefore an unknown field, not a known nested path.
	if strings.Contains(name, ".") {
		w.fallback(path, raw)
		return
	}
	key := name
	if parentKey != "" {
		key = parentKey + "." + name
	}
	kind, known := resultFields[key]
	if !known {
		w.fallback(path, raw)
		return
	}
	if kind == fieldToken && (len(raw) == 0 || raw[0] != '"') {
		w.fallback(path, raw)
		return
	}
	if kind == fieldObject {
		if bytes.Equal(raw, []byte("null")) {
			w.field(key, "null")
			return
		}
		members, err := resultMembers(raw)
		if err != nil {
			w.fallback(path, raw)
			return
		}
		w.objectMembers(key, path, raw, members)
		return
	}
	if kind == fieldBlock {
		if block, ok := readableResultBlock(key, raw, w.observationID); ok {
			if key == "inspection" {
				members, _ := resultMembers(raw)
				for _, m := range members {
					if m.name == "origin" {
						w.line("inspection.origin=" + resultScalar(m.value, fieldToken))
					}
				}
			}
			w.block(block)
			return
		}
		w.fallback(path, raw)
		return
	}
	w.field(key, resultScalar(raw, kind))
}

func (w *resultTextWriter) objectMembers(
	parentKey, path string, raw []byte, members []resultMember,
) {
	if len(members) == 0 {
		w.fallback(path, raw)
		return
	}
	for _, member := range members {
		w.member(parentKey, member.name, path+"/"+pointerPart(member.name), member.value)
	}
}

func resultScalar(raw []byte, kind resultFieldKind) string {
	if kind == fieldToken && len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil && resultToken(s) {
			return s
		}
	}
	return compactResultJSON(raw)
}
