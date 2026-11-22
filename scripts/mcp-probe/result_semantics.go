package main

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"math"
	"math/big"
	"strconv"
	"time"
)

// This file holds the probe's own JSON semantics: an exact decoder that keeps
// number literals instead of rounding them through float64, and a structural
// comparison that ignores object member order while preserving array order and
// numeric value. The probe never compares source fingerprints with the bytes
// an SDK produced from a re-serialized tools/list response; it compares meaning
// and only then relies on the source fingerprint.

var (
	errJSONInvalid     = errors.New("json invalid")
	errJSONTrailing    = errors.New("json trailing value")
	errJSONDuplicate   = errors.New("json duplicate member")
	errJSONScalarShape = errors.New("json scalar shape")
)

// decodeExactJSON decodes exactly one JSON value. Object member order is not
// significant; array order and the number literal are preserved as
// encoding/json.Number.
func decodeExactJSON(data []byte) (any, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	value, err := readExactJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errJSONTrailing
	}
	return value, nil
}

// readExactJSONValue reads one token tree. Duplicate object names are rejected
// so a readable projection can never be resolved by last-member-wins.
func readExactJSONValue(decoder *jsontext.Decoder) (any, error) {
	switch decoder.PeekKind() {
	case '{':
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		object := make(map[string]any)
		for decoder.PeekKind() != '}' {
			name, err := decoder.ReadToken()
			if err != nil {
				return nil, err
			}
			if name.Kind() != '"' {
				return nil, errJSONInvalid
			}
			key := name.String()
			if _, duplicate := object[key]; duplicate {
				return nil, errJSONDuplicate
			}
			value, err := readExactJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		array := make([]any, 0)
		for decoder.PeekKind() != ']' {
			value, err := readExactJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		return array, nil
	case '"':
		token, err := decoder.ReadToken()
		if err != nil {
			return nil, err
		}
		return token.String(), nil
	case 't', 'f':
		token, err := decoder.ReadToken()
		if err != nil {
			return nil, err
		}
		if token.Kind() != 't' && token.Kind() != 'f' {
			return nil, errJSONScalarShape
		}
		return token.Bool(), nil
	case 'n':
		if _, err := decoder.ReadToken(); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		token, err := decoder.ReadToken()
		if err != nil {
			return nil, err
		}
		if token.Kind() != '0' {
			return nil, errJSONScalarShape
		}
		return json.Number(token.String()), nil
	}
}

// semanticEqual reports whether two decoded JSON values mean the same thing.
// Object member order is ignored, array order is significant, and numbers are
// compared by exact rational value rather than float64 representation.
func semanticEqual(left, right any) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	switch leftValue := left.(type) {
	case map[string]any:
		rightValue, ok := right.(map[string]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false
		}
		for key, value := range leftValue {
			other, exists := rightValue[key]
			if !exists || !semanticEqual(value, other) {
				return false
			}
		}
		return true
	case []any:
		rightValue, ok := right.([]any)
		if !ok || len(leftValue) != len(rightValue) {
			return false
		}
		for index := range leftValue {
			if !semanticEqual(leftValue[index], rightValue[index]) {
				return false
			}
		}
		return true
	case string:
		rightValue, ok := right.(string)
		return ok && leftValue == rightValue
	case bool:
		rightValue, ok := right.(bool)
		return ok && leftValue == rightValue
	default:
		leftNumber, leftOK := numberRat(left)
		rightNumber, rightOK := numberRat(right)
		return leftOK && rightOK && leftNumber.Cmp(rightNumber) == 0
	}
}

// numberRat converts every number representation the probe can meet into an
// exact rational. float64 input is accepted only as the exact value it already
// represents; it cannot recover precision lost before it reached the probe.
func numberRat(value any) (*big.Rat, bool) {
	switch number := value.(type) {
	case json.Number:
		rational, ok := new(big.Rat).SetString(string(number))
		return rational, ok
	case int:
		return new(big.Rat).SetInt64(int64(number)), true
	case int64:
		return new(big.Rat).SetInt64(number), true
	case uint64:
		return new(big.Rat).SetUint64(number), true
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, false
		}
		rational, ok := new(big.Rat).SetString(strconv.FormatFloat(number, 'g', -1, 64))
		return rational, ok
	default:
		return nil, false
	}
}

// jsonStringField is a small helper for exact-decoded objects.
func jsonStringField(object map[string]any, name string) (string, bool) {
	value, found := object[name]
	if !found {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

// exactObject decodes data and requires an object at the top level.
func exactObject(data []byte) (map[string]any, error) {
	value, err := decodeExactJSON(data)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errJSONInvalid
	}
	return object, nil
}

// ---------------------------------------------------------------------------
// result v4 -> structured semantic consistency

// compareResultTextToStructured applies every declared result v4 omission to
// the structured copy and then requires the two remaining objects to mean the
// same thing. effect and observation.quality are deliberately structured-only;
// the text parser rejects them before this comparison. Unknown/future members
// are never stripped: a field that is not in the explicit omission table must
// survive the text projection.
func compareResultTextToStructured(decoded *decodedTextResult,
	structured map[string]any, view resultView, requestRef string) *checkError {
	text := deepCloneObject(decoded.object)
	want := deepCloneObject(structured)
	delete(want, "effect")

	// outcome: omitted only when !isError && executed && no operation_state.
	if textOutcome, present := text["outcome"]; !present {
		if wantOutcome, ok := want["outcome"]; ok {
			wantText, isText := wantOutcome.(string)
			if !isText {
				return failf("both_mode_outcome_mismatch")
			}
			switch {
			case wantText == "":
				delete(want, "outcome")
			case wantText == outcomeExecuted && !decoded.isError && decoded.header.State == "":
				delete(want, "outcome")
			default:
				return failf("both_mode_outcome_mismatch", field("structured", wantText))
			}
		}
	} else if _, ok := want["outcome"]; !ok {
		_ = textOutcome
		return failf("both_mode_outcome_mismatch")
	}

	textObs, textHasObs := text["observation"].(map[string]any)
	wantObs, wantHasObs := want["observation"].(map[string]any)
	if textHasObs != wantHasObs {
		return failf("both_mode_observation_mismatch")
	}
	if wantHasObs {
		if _, ok := textObs["status"]; !ok {
			textObs["status"] = observationComplete
		}
		// Always omitted: origin, revision and the server-side input deadline.
		delete(wantObs, "origin")
		delete(wantObs, "revision")
		delete(wantObs, "actionable_until")
		delete(wantObs, "quality")
		_, hasTree := wantObs["tree"]
		if failure := compareScreenFields(textObs, wantObs, hasTree, view.Session != nil); failure != nil {
			return failure
		}
		normalizeTimestampField(textObs, "captured_at", wantObs, "captured_at")
		if failure := normalizeImageView(textObs, wantObs, "image"); failure != nil {
			return failure
		}
		reduceTreeHeader(wantObs)
	}

	if failure := compareInspection(text, want); failure != nil {
		return failure
	}
	if failure := compareAppSection(text, want); failure != nil {
		return failure
	}
	if failure := compareRecoverySection(text, want, view, requestRef); failure != nil {
		return failure
	}
	normalizeSessionTimestamps(text, want)
	if failure := normalizeImageView(text, want, "image"); failure != nil {
		return failure
	}
	normalizeErrorObjects(text, want)

	if !semanticEqual(text, want) {
		return failf("both_mode_object_mismatch")
	}
	return nil
}

func deepCloneObject(object map[string]any) map[string]any {
	clone := make(map[string]any, len(object))
	for key, value := range object {
		clone[key] = deepCloneValue(value)
	}
	return clone
}

func deepCloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return deepCloneObject(typed)
	case []any:
		clone := make([]any, len(typed))
		for index, item := range typed {
			clone[index] = deepCloneValue(item)
		}
		return clone
	default:
		return value
	}
}

// compareScreenFields implements the conditional screen omission. The full
// width/height/display_id triple is carried only for a frame with a non-empty
// session or without a tree; a non-zero rotation is always compared.
func compareScreenFields(textObs, wantObs map[string]any, hasTree, sessionPresent bool) *checkError {
	requireFull := !hasTree || sessionPresent
	textScreen, textHas := textObs["screen"].(map[string]any)
	wantScreen, wantHas := wantObs["screen"].(map[string]any)
	if !wantHas {
		if textHas {
			return failf("both_mode_screen_unexpected")
		}
		return nil
	}
	if !textHas {
		if requireFull {
			return failf("both_mode_screen_missing")
		}
		rotation, _ := exactIntegerValue(wantScreen["rotation"])
		if rotation != 0 {
			return failf("both_mode_screen_missing", field("rotation", strconv.FormatInt(rotation, 10)))
		}
		delete(wantObs, "screen")
		return nil
	}
	if rotation, ok := exactIntegerValue(textScreen["rotation"]); ok {
		wantRotation, _ := exactIntegerValue(wantScreen["rotation"])
		if rotation != wantRotation {
			return failf("both_mode_screen_rotation_mismatch")
		}
	} else if _, exists := wantScreen["rotation"]; exists {
		if rotation, _ := exactIntegerValue(wantScreen["rotation"]); rotation != 0 {
			return failf("both_mode_screen_rotation_mismatch")
		}
	}
	if !requireFull {
		delete(wantScreen, "width")
		delete(wantScreen, "height")
		delete(wantScreen, "display_id")
		delete(textScreen, "width")
		delete(textScreen, "height")
		delete(textScreen, "display_id")
	}
	return nil
}

func exactIntegerValue(value any) (int64, bool) {
	number, ok := exactInteger(value)
	return number, ok
}

// normalizeImageView applies the JPEG media-type omission and nested screen
// defaults to one image metadata object.
func normalizeImageView(textParent, wantParent map[string]any, key string) *checkError {
	textImage, textHas := textParent[key].(map[string]any)
	wantImage, wantHas := wantParent[key].(map[string]any)
	if !wantHas {
		if textHas {
			return failf("both_mode_image_unexpected", field("path", key))
		}
		return nil
	}
	if !textHas {
		return failf("both_mode_image_missing", field("path", key))
	}
	if failure := normalizeImageObject(textImage, wantImage); failure != nil {
		return failure
	}
	return nil
}

func normalizeImageObject(textImage, wantImage map[string]any) *checkError {
	if media, ok := wantImage["media_type"].(string); ok {
		if media == imageJPEG {
			delete(wantImage, "media_type")
			delete(textImage, "media_type")
		}
	}
	normalizeTimestampField(textImage, "captured_at", wantImage, "captured_at")
	textScreen, textHas := textImage["screen"].(map[string]any)
	wantScreen, wantHas := wantImage["screen"].(map[string]any)
	if !wantHas {
		if textHas {
			return failf("both_mode_image_screen_unexpected")
		}
		return nil
	}
	if !textHas {
		return failf("both_mode_image_screen_missing")
	}
	if rotation, ok := exactIntegerValue(textScreen["rotation"]); ok {
		wantRotation, _ := exactIntegerValue(wantScreen["rotation"])
		if rotation != wantRotation {
			return failf("both_mode_image_screen_rotation_mismatch")
		}
	}
	return nil
}

// normalizeTimestampField truncates both known timestamp fields to UTC
// HH:MM:SS. A non-RFC3339 value is compared literally, so no data is invented.
func normalizeTimestampField(textParent map[string]any, textKey string,
	wantParent map[string]any, wantKey string) {
	textValue, textOK := textParent[textKey]
	wantValue, wantOK := wantParent[wantKey]
	if !textOK || !wantOK {
		return
	}
	textParent[textKey] = normalizeClockValue(textValue)
	wantParent[wantKey] = normalizeClockValue(wantValue)
}

func normalizeClockValue(value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	if validClockTime(text) {
		return text
	}
	if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return parsed.UTC().Format("15:04:05")
	}
	return text
}

func normalizeSessionTimestamps(text, want map[string]any) {
	textSession, textHas := text["session"].(map[string]any)
	wantSession, wantHas := want["session"].(map[string]any)
	if !textHas || !wantHas {
		return
	}
	delete(wantSession, "agent")
	for _, field := range []string{"started_at", "expires_at", "ended_at"} {
		normalizeTimestampField(textSession, field, wantSession, field)
	}
}

// reduceTreeHeader strips the block body while keeping every header member, so
// an unknown tree header field still breaks equality.
func reduceTreeHeader(wantObs map[string]any) {
	tree, ok := wantObs["tree"].(map[string]any)
	if !ok {
		return
	}
	for _, bodyKey := range []string{
		"root", "set", "add", "remove", "children", "replace",
	} {
		delete(tree, bodyKey)
	}
}

// compareInspection reduces the structured inspection to the header facts the
// inspect v3 block declares (origin is always omitted by v4).
func compareInspection(text, want map[string]any) *checkError {
	wantInspection, wantHas := want["inspection"].(map[string]any)
	textInspection, textHas := text["inspection"].(map[string]any)
	if wantHas != textHas {
		return failf("both_mode_inspection_mismatch")
	}
	if !wantHas {
		return nil
	}
	header := make(map[string]any, 6)
	for _, key := range []string{"observation_id", "revision", "captured_at", "scope", "view"} {
		if value, exists := wantInspection[key]; exists {
			header[key] = value
		}
	}
	if tree, ok := wantInspection["tree"].(map[string]any); ok {
		if view, exists := tree["view"]; exists {
			header["tree"] = map[string]any{"view": view}
		}
	}
	if capturedAt, exists := header["captured_at"]; exists {
		textValue, _ := textInspection["captured_at"]
		header["captured_at"] = normalizeClockValue(capturedAt)
		textInspection["captured_at"] = normalizeClockValue(textValue)
	}
	want["inspection"] = header
	return nil
}

// compareAppSection normalizes the shared app timestamp and leaves every other
// member, including the nullable/empty-array tri-states, for exact comparison.
func compareAppSection(text, want map[string]any) *checkError {
	textApp, textHas := text["app"].(map[string]any)
	wantApp, wantHas := want["app"].(map[string]any)
	if textHas != wantHas {
		return failf("both_mode_app_mismatch")
	}
	if !wantHas {
		return nil
	}
	normalizeTimestampField(textApp, "captured_at", wantApp, "captured_at")
	return nil
}

// compareRecoverySection validates the recovery line separately because result
// v4 may replace the abstract O/P kind with a semantically grounded exact ID.
func compareRecoverySection(text, want map[string]any, view resultView, requestRef string) *checkError {
	textRecovery, textHas := text["recovery"].(map[string]any)
	wantRecovery, wantHas := want["recovery"].(map[string]any)
	if textHas != wantHas {
		return failf("both_mode_recovery_mismatch")
	}
	if !wantHas {
		return nil
	}
	for _, key := range []string{"entry", "input_state", "cause"} {
		textValue, textOK := textRecovery[key]
		wantValue, wantOK := wantRecovery[key]
		if textOK != wantOK || !semanticEqual(textValue, wantValue) {
			return failf("both_mode_recovery_mismatch", field("field", key))
		}
	}
	textRef, _ := textRecovery["reference"].(string)
	wantRef, _ := wantRecovery["reference"].(string)
	if failure := checkRecoveryReference(textRef, wantRef, view, requestRef); failure != nil {
		return failure
	}
	delete(text, "recovery")
	delete(want, "recovery")
	return nil
}

// checkRecoveryReference accepts either the safe abstract kind or an exact ID
// that the probe's own independent resolver derives from this call's facts.
func checkRecoveryReference(textRef, structuredRef string, view resultView,
	requestRef string) *checkError {
	if structuredRef == "none" {
		if textRef != "none" {
			return failf("both_mode_recovery_ref_mismatch", field("text", textRef))
		}
		return nil
	}
	if textRef == structuredRef {
		return nil
	}
	expected := resolveRecoveryReference(view, requestRef)
	if textRef == expected && isExactReference(textRef) {
		return nil
	}
	return failf("both_mode_recovery_ref_mismatch",
		field("text", textRef), field("structured", structuredRef), field("expected", expected))
}

func isExactReference(value string) bool {
	if len(value) < 2 || (value[0] != 'O' && value[0] != 'P') {
		return false
	}
	return validReference(value, value[0])
}

// resolveRecoveryReference reproduces the documented result v4 R1 resolution
// rules. It is independent of internal/mcp and never guesses: every fallback
// returns the abstract kind.
func resolveRecoveryReference(view resultView, requestRef string) string {
	if view.Recovery == nil {
		return ""
	}
	recovery := view.Recovery
	reference := recovery.Reference
	if reference == "none" {
		return "none"
	}
	errCode := ""
	if view.Error != nil {
		errCode = view.Error.Code
	} else if view.Observation != nil && view.Observation.Error != nil {
		errCode = view.Observation.Error.Code
	}
	switch {
	case recovery.Entry == "retry" && reference == "O":
		if view.Observation != nil && isObsID(view.Observation.ID) {
			return view.Observation.ID
		}
		if isObsID(requestRef) {
			return requestRef
		}
		return "O"
	case (recovery.Entry == "status" || recovery.Entry == "end") && reference == "O":
		if isObsID(view.TargetReference) {
			return view.TargetReference
		}
		if isObsID(requestRef) {
			return requestRef
		}
		return "O"
	case recovery.Entry == "status" && reference == "P":
		if errCode == "device_busy" {
			if isOpID(view.BlockingOperationID) {
				return view.BlockingOperationID
			}
			return "P"
		}
		if errCode == "reference_has_no_session" || errCode == "operation_unknown" ||
			errCode == "operation_not_found" ||
			(errCode == "request_closed" && view.RequestClosed == nil) {
			if isOpID(view.TargetReference) {
				return view.TargetReference
			}
			if isOpID(requestRef) {
				return requestRef
			}
			return "P"
		}
		if isOpID(view.OperationID) &&
			(view.Outcome == "uncertain" ||
				view.Outcome == outcomePartial ||
				view.OperationState == "running" ||
				view.Phase == "executed_revoked" ||
				(view.RequestClosed != nil && *view.RequestClosed) ||
				oneOf(recovery.Cause, "running", "executed_revoked", "cleanup_pending") ||
				oneOf(errCode, "call_cancelled", "transport_unavailable", "invalid_response", "task_lifecycle_busy",
					"text_submit_not_executed") ||
				(view.Cleanup != nil &&
					(view.Cleanup.State == "pending" || view.Cleanup.State == "unconfirmed"))) {
			return view.OperationID
		}
		if isOpID(view.StartOperation) {
			return view.StartOperation
		}
		if view.Session != nil && isOpID(view.Session.Origin) {
			return view.Session.Origin
		}
		if isOpID(view.TargetReference) {
			return view.TargetReference
		}
		return "P"
	case recovery.Entry == "observe" && reference == "P":
		// A confirmed partial text act owns the observe recovery even when a
		// frozen copy still carries session.origin: the durable
		// observe(P, retired) must resolve to this operation's own P both when
		// the first response published a fresh ready O and when that
		// observation failed or was never attached.
		if (view.Outcome == outcomePartial || errCode == "text_submit_not_executed") &&
			isOpID(view.OperationID) {
			return view.OperationID
		}
		if view.Session != nil && isOpID(view.Session.Origin) {
			return view.Session.Origin
		}
		if isOpID(requestRef) &&
			!oneOf(errCode, "operation_unknown", "operation_not_found", "request_closed",
				"reference_has_no_session") {
			return requestRef
		}
		if isOpID(view.OperationID) &&
			(recovery.Cause == "post_observation_failed" ||
				view.Phase == "post_observation_failed" ||
				view.Outcome == "uncertain" ||
				view.Outcome == outcomePartial ||
				oneOf(errCode, "observation_expired", "observation_claimed",
					"observation_retired", "hierarchy_unavailable", "stale_screen",
					"tree_too_large", "control_ui_on_virtual_display", "display_unavailable",
					"text_submit_not_executed")) {
			return view.OperationID
		}
		return "P"
	case recovery.Entry == "end" && reference == "P":
		if view.Error != nil && view.Error.Details != nil {
			if raw, exists := view.Error.Details["start_operation_id"]; exists {
				var value string
				if json.Unmarshal(raw, &value) == nil && isOpID(value) {
					return value
				}
			}
		}
		if view.Session != nil && isOpID(view.Session.Origin) {
			return view.Session.Origin
		}
		if isOpID(view.StartOperation) {
			return view.StartOperation
		}
		if isOpID(requestRef) {
			return requestRef
		}
		return "P"
	}
	return reference
}

func isObsID(value string) bool {
	return len(value) >= 2 && value[0] == 'O' && resultToken(value)
}

func isOpID(value string) bool {
	return len(value) >= 2 && value[0] == 'P' && resultToken(value)
}

func oneOf(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

// normalizeErrorObjects reconciles the fixed v4 error line shape with a
// structured error that omits an empty message member. A non-empty structured
// message is never invented or dropped.
func normalizeErrorObjects(text, want map[string]any) {
	pairs := [][2][]string{
		{{"error"}, {"error"}},
		{{"notification", "error"}, {"notification", "error"}},
		{{"observation", "error"}, {"observation", "error"}},
		{{"observation", "image", "error"}, {"observation", "image", "error"}},
		{{"image", "error"}, {"image", "error"}},
		{{"cleanup", "error"}, {"cleanup", "error"}},
	}
	for _, pair := range pairs {
		textError, textOK := lookupPath(text, pair[0])
		wantError, wantOK := lookupPath(want, pair[1])
		if !textOK || !wantOK {
			continue
		}
		textObject, textIsObject := textError.(map[string]any)
		wantObject, wantIsObject := wantError.(map[string]any)
		if !textIsObject || !wantIsObject {
			continue
		}
		if message, ok := textObject["message"].(string); ok && message == "" {
			if _, present := wantObject["message"]; !present {
				wantObject["message"] = ""
			}
		}
	}
	if wantApp, ok := want["app"].(map[string]any); ok {
		if tasks, ok := wantApp["tasks"].([]any); ok {
			for _, task := range tasks {
				taskObject, _ := task.(map[string]any)
				if errorObject, ok := taskObject["error"].(map[string]any); ok {
					if _, present := errorObject["message"]; !present {
						errorObject["message"] = ""
					}
				}
			}
		}
	}
}
