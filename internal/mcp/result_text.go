package mcp

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strconv"
	"strings"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type resultFieldKind uint8

const (
	fieldToken resultFieldKind = iota + 1
	fieldString
	fieldJSON
	fieldObject
	fieldBlock
	fieldLiteral // token whose JSON type is boolean, number or null
)

// Single classification table for the encoder, test decoder and manifest gate.
// Composite fields rendered as JSON retain all nested members automatically.
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
	add("text_input.", "write enter", fieldToken)
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
	add("observation.", "status id origin revision captured_at actionable_until", fieldToken)
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

type resultMember struct {
	name  string
	value json.RawMessage
}

// Read members in wire order: Result's public order is significant for the
// readable projection; converting the input object to a map would lose it.
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

func resultScalar(raw []byte, kind resultFieldKind) string {
	if kind == fieldToken && len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil && resultToken(s) {
			return s
		}
	}
	return compactResultJSON(raw)
}

func pointerPart(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func quoteResult(s string) string { b, _ := jsonv2.Marshal(s); return string(b) }

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

func encodeResultText(raw []byte, isError bool) (string, error) {
	members, err := resultMembers(raw)
	if err != nil {
		return "", err
	}
	observationMembers, hasObservation := resultObservationObject(members)
	w := &resultTextWriter{
		groupObservation: true,
		observationID:    resultObservationID(observationMembers),
	}
	w.line("result v3 is_error=" + strconv.FormatBool(isError))
	for _, m := range members {
		path := "/" + pointerPart(m.name)
		if hasObservation && m.name == "observation" {
			w.objectMembers("observation", path, m.value, observationMembers)
			continue
		}
		w.member("", m.name, path, m.value)
	}
	w.line("end result")
	return w.String(), nil
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

func resultObservationObject(members []resultMember) ([]resultMember, bool) {
	for _, member := range members {
		if member.name == "observation" {
			observation, err := resultMembers(member.value)
			return observation, err == nil
		}
	}
	return nil, false
}

func resultObservationID(observation []resultMember) string {
	id := ""
	for _, member := range observation {
		if member.name != "id" {
			continue
		}
		if id != "" || json.Unmarshal(member.value, &id) != nil {
			return ""
		}
	}
	if !validateV4ObservationToken(id) {
		return ""
	}
	return id
}

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
	members, err := resultMembers(raw)
	if err != nil {
		return nil, nil, false
	}
	m := map[string]json.RawMessage{}
	for _, v := range members {
		m[v.name] = v.value
	}
	return members, m, true
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
