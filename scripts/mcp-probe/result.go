package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strconv"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The probe compares stable public values only.
const (
	resultModeBoth       = "both"
	resultModeText       = "text"
	resultModeStructured = "structured"

	outcomeExecuted     = "executed"
	outcomePartial      = "partial"
	sessionActive       = "active"
	sessionStarting     = "starting"
	sessionEnded        = "ended"
	observationComplete = "complete"
	observationPartial  = "partial"
	operationFinished   = "finished"
	operationRunning    = "running"
	operationNotSeen    = "not_seen"
	requestClosedLabel  = "request_closed"
	treeFull            = "full"
	treeDelta           = "delta"
	imageJPEG           = "image/jpeg"

	qualityUnknown        = "unknown"
	qualityStopNotSampled = "not_sampled"
	waitTriggerTimeout    = "timeout"

	textInputNotExecuted = "not_executed"
	textInputUncertain   = "uncertain"
	textInputExecuted    = "executed"

	// The three published zero-dispatch recovery strategy names. The probe
	// repeats them instead of importing protocol constants so the live wire is
	// compared with an independent copy of the contract.
	autoRecoveryTransientRetry  = "transient_retry"
	autoRecoveryTextRebind      = "text_rebind"
	autoRecoveryContainerRebind = "container_rebind"
)

var autoRecoveryStrategies = map[string]bool{
	autoRecoveryTransientRetry:  true,
	autoRecoveryTextRebind:      true,
	autoRecoveryContainerRebind: true,
}

// Frozen public enumerations of the act quality/effect projection. The probe
// repeats them instead of importing protocol constants so the live wire is
// compared with an independent copy of the contract.
var (
	qualityStates = map[string]bool{
		"stable": true, "unstable": true, "unknown": true,
	}
	qualityStopReasons = map[string]bool{
		"stable": true, "max_samples": true, "budget": true,
		"unreadable": true, "not_sampled": true,
		"condition": true, "timeout": true,
	}
	effectStates = map[string]bool{
		"observed": true, "not_observed": true, "unknown": true,
	}
)

// sessionView is the public session projection of one structured result.
type sessionView struct {
	Origin    string `json:"origin"`
	State     string `json:"state"`
	Mode      string `json:"mode"`
	Agent     string `json:"agent"`
	StartedAt string `json:"started_at"`
	ExpiresAt string `json:"expires_at"`
	EndedAt   string `json:"ended_at"`
	Reason    string `json:"reason"`
}

// errorView is the controlled error object. Details stay raw so a check can
// compare task ids and other controlled values exactly.
type errorView struct {
	Code    string                    `json:"code"`
	Message string                    `json:"message"`
	Details map[string]jsontext.Value `json:"details"`
}

// recoveryView is the controlled next-step projection.
type recoveryView struct {
	Entry      string `json:"entry"`
	Reference  string `json:"reference"`
	InputState string `json:"input_state"`
	Cause      string `json:"cause"`
}

// imageView is the observation or top-level snap image metadata carried by
// structuredContent.
type imageView struct {
	CapturedAt string      `json:"captured_at"`
	Width      int         `json:"width"`
	Height     int         `json:"height"`
	MediaType  string      `json:"media_type"`
	ReadOnly   bool        `json:"read_only"`
	Screen     *screenView `json:"screen"`
	Error      *errorView  `json:"error"`
}

// screenView is the admission display snapshot carried by a snap image or a
// full observation frame.
type screenView struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Rotation  int  `json:"rotation"`
	DisplayID *int `json:"display_id"`
}

type shellView struct {
	Termination string     `json:"termination"`
	ExitCode    *int       `json:"exit_code"`
	Output      *string    `json:"output"`
	Truncated   *bool      `json:"truncated"`
	OutputError *errorView `json:"output_error"`
}

type diagnosisEvent struct {
	At    string `json:"at"`
	Phase string `json:"phase"`
	Code  string `json:"code"`
}

type diagnosisView struct {
	Timeline    []diagnosisEvent `json:"timeline"`
	ErrorCounts map[string]int   `json:"error_counts"`
}

// treeView keeps only the tree format and delta base.
type treeView struct {
	Format string `json:"format"`
	Base   string `json:"base"`
}

// inspectionView keeps only the inspect header fields the probe asserts.
type inspectionView struct {
	ObservationID string `json:"observation_id"`
	Scope         string `json:"scope"`
	Revision      int    `json:"revision"`
}

// qualityView is the settle quality of one observation. All three members are
// explicit facts; samples=0 with stop_reason=not_sampled is valid and must be
// carried, not omitted.
type qualityView struct {
	State      string `json:"state"`
	Samples    int    `json:"samples"`
	StopReason string `json:"stop_reason"`
}

// effectView is the bounded scroll-effect fact. Only a scroll act result
// carries it; a tap/key result must not.
type effectView struct {
	State string `json:"state"`
}

// waitView is the bounded condition-wait outcome. A frozen trigger and an
// elapsed time are always carried; the matched list is omitted when empty and
// is never null, so a decoded null is indistinguishable from an absent member.
type waitView struct {
	Trigger   string   `json:"trigger"`
	Matched   []string `json:"matched"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

// observationView is the observation header; tree bodies are not interpreted.
type observationView struct {
	Status     string          `json:"status"`
	ID         string          `json:"id"`
	Origin     string          `json:"origin"`
	Revision   int             `json:"revision"`
	CapturedAt string          `json:"captured_at"`
	Visual     string          `json:"visual"`
	TreeChange string          `json:"tree_change"`
	Hint       string          `json:"hint"`
	Quality    *qualityView    `json:"quality"`
	Screen     *screenView     `json:"screen"`
	Foreground *foregroundView `json:"foreground"`
	Missing    []string        `json:"missing"`
	Tree       *treeView       `json:"tree"`
	Image      *imageView      `json:"image"`
	Error      *errorView      `json:"error"`
}

// notificationView is the notify delivery fact carried by the v4 aggregate
// notify line and its optional error sub-line.
type notificationView struct {
	State string     `json:"state"`
	Error *errorView `json:"error"`
}

type cleanupView struct {
	State     string     `json:"state"`
	Execution string     `json:"execution"`
	Tasks     string     `json:"tasks"`
	Recents   string     `json:"recents"`
	Display   string     `json:"display"`
	Error     *errorView `json:"error"`
}

// textInputView is the fixed composite-text stage snapshot. Both members must
// be present on the wire; the probe treats a partial object as a failure. The
// submission key is named submit because a submission is one bounded ENTER or
// one node tap.
type textInputView struct {
	Write  string `json:"write"`
	Submit string `json:"submit"`
}

// autoRecoveryView is the explicit zero-dispatch recovery marker of an action
// that reached dispatch anyway. strategy and attempts are mandatory; the two
// public node refs are omitted for a coordinate-only request.
type autoRecoveryView struct {
	Strategy     string `json:"strategy"`
	OriginalRef  string `json:"original_ref"`
	RecoveredRef string `json:"recovered_ref"`
	Attempts     int    `json:"attempts"`
}

// resultView is the probe's read-only projection of structuredContent.
type resultView struct {
	OperationID         string            `json:"operation_id"`
	OperationState      string            `json:"operation_state"`
	Outcome             string            `json:"outcome"`
	Phase               string            `json:"phase"`
	TextInput           *textInputView    `json:"text_input"`
	AutoRecovery        *autoRecoveryView `json:"auto_recovery"`
	Effect              *effectView       `json:"effect"`
	TargetReference     string            `json:"target_reference"`
	RequestClosed       *bool             `json:"request_closed"`
	CanStart            *bool             `json:"can_start"`
	BlockingCode        string            `json:"blocking_code"`
	StartOperation      string            `json:"start_operation_id"`
	ObservationState    string            `json:"observation_state"`
	BlockingOperationID string            `json:"blocking_operation_id"`
	Session             *sessionView      `json:"session"`
	Image               *imageView        `json:"image"`
	Observation         *observationView  `json:"observation"`
	Notification        *notificationView `json:"notification"`
	MessageID           string            `json:"message_id"`
	Saved               *bool             `json:"saved"`
	Inspection          *inspectionView   `json:"inspection"`
	App                 *appView          `json:"app"`
	Shell               *shellView        `json:"shell"`
	Wait                *waitView         `json:"wait"`
	Diagnosis           *diagnosisView    `json:"diagnosis"`
	Cleanup             *cleanupView      `json:"cleanup"`
	Error               *errorView        `json:"error"`
	Recovery            *recoveryView     `json:"recovery"`
}

// checkError is one failed assertion with controlled reason and fields.
type checkError struct {
	reason string
	fields []string
}

func failf(reason string, fields ...string) *checkError {
	return &checkError{reason: reason, fields: fields}
}

// callResult is one projected tool result. structured is the canonical
// structuredContent JSON, kept for leak scanning; object is the exact decoded
// JSON projection used for field-by-field comparison across channels.
type callResult struct {
	result     *sdk.CallToolResult
	view       resultView
	object     map[string]any
	structured []byte
	texts      []string
	images     []*sdk.ImageContent
	// textOnly records that this projection came from the result v4 text
	// channel. Only then may a missing explicit fact be interpreted through the
	// v4 default/omission rules.
	textOnly bool
	// textHeader is the parsed v4 header, kept for omission semantics.
	textHeader *resultHeader
	// requestRef is the single-call reference extracted from the tool
	// arguments. It is used only to validate an exact recovery ref.
	requestRef string
}

// project decodes a tool result into the probe's projection without printing
// any content. The selected result mode is checked at the same time so an
// ordinary probe never accepts a channel that the mode must omit. The optional
// requestRef is the caller-supplied reference of this same call; it lets the
// both-mode gate prove that an exact recovery ref is semantically grounded.
func project(result *sdk.CallToolResult, mode string, requestRefs ...string) (*callResult, *checkError) {
	if result == nil {
		return nil, failf("empty_result")
	}
	if mode != resultModeBoth && mode != resultModeText && mode != resultModeStructured {
		return nil, failf("invalid_result_mode")
	}
	requestRef := ""
	if len(requestRefs) > 0 {
		requestRef = requestRefs[0]
	}
	projected := &callResult{result: result, requestRef: requestRef}
	for _, content := range result.Content {
		switch value := content.(type) {
		case *sdk.TextContent:
			projected.texts = append(projected.texts, value.Text)
		case *sdk.ImageContent:
			projected.images = append(projected.images, value)
		}
	}
	var structuredView resultView
	var structuredObject map[string]any
	if result.StructuredContent != nil {
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil || string(encoded) == "null" {
			return nil, failf("structured_content_unusable")
		}
		projected.structured = encoded
		if structuredObject, err = exactObject(encoded); err != nil {
			return nil, failf("structured_content_unusable")
		}
		if err := json.Unmarshal(encoded, &structuredView); err != nil {
			return nil, failf("structured_content_unusable")
		}
	}

	switch mode {
	case resultModeText:
		if result.StructuredContent != nil {
			return nil, failf("text_mode_has_structured_content")
		}
		if len(projected.texts) != 1 {
			return nil, failf("text_mode_result_text_count")
		}
		decoded, failure := decodeResultText(projected.texts[0])
		if failure != nil {
			return nil, failure
		}
		if decoded.isError != result.IsError {
			return nil, failf("text_mode_is_error_mismatch")
		}
		projected.view = decoded.view
		projected.object = decoded.object
		projected.textOnly = true
		projected.textHeader = &decoded.header
	case resultModeStructured:
		if result.StructuredContent == nil {
			return nil, failf("structured_mode_content_missing")
		}
		if result.IsError {
			if failure := checkDiagnosticText(projected.texts); failure != nil {
				return nil, failure
			}
		} else {
			if result.Content == nil {
				return nil, failf("structured_mode_content_null")
			}
			if len(projected.texts) != 0 {
				return nil, failf("structured_mode_has_result_text")
			}
		}
		projected.view = structuredView
		projected.object = structuredObject
	case resultModeBoth:
		if result.StructuredContent == nil {
			return nil, failf("both_mode_content_missing")
		}
		if len(projected.texts) != 1 {
			return nil, failf("both_mode_result_text_count")
		}
		decoded, failure := decodeResultText(projected.texts[0])
		if failure != nil {
			return nil, failure
		}
		if decoded.isError != result.IsError {
			return nil, failf("both_mode_is_error_mismatch")
		}
		// The v4 text channel is a declared projection, not a byte-for-byte
		// mirror of structuredContent. The gate applies every explicit omission
		// rule, compares the complete remaining object (unknown fields included)
		// and validates any exact recovery ref against the same-call facts.
		if failure := compareResultTextToStructured(
			decoded, structuredObject, structuredView, requestRef); failure != nil {
			return nil, failure
		}
		projected.view = structuredView
		projected.object = structuredObject
		projected.textHeader = &decoded.header
	}
	if failure := checkTextInputSnapshot(projected.view.TextInput); failure != nil {
		return nil, failure
	}
	if failure := checkAutoRecovery(projected.view.AutoRecovery); failure != nil {
		return nil, failure
	}
	// Every result mode shares the checks on decoded observation metadata and
	// the tree projection.
	if failure := checkProjectionConsistency(projected); failure != nil {
		return nil, failure
	}
	return projected, nil
}

// checkTextInputSnapshot validates the fixed composite-text stage object. A
// result without the object is valid; a present object must carry both members
// with one of the five legal stage combinations.
func checkTextInputSnapshot(input *textInputView) *checkError {
	if input == nil {
		return nil
	}
	valid := func(value string) bool {
		return value == textInputNotExecuted ||
			value == textInputUncertain || value == textInputExecuted
	}
	if !valid(input.Write) || !valid(input.Submit) {
		return failf("text_input_state_invalid")
	}
	if input.Submit != textInputNotExecuted && input.Write != textInputExecuted {
		return failf("text_input_stage_order_invalid")
	}
	return nil
}

// checkAutoRecovery validates the explicit zero-dispatch recovery marker. An
// absent object is valid; a present object must name one of the three published
// strategies, keep attempts >= 1, and carry either both public node refs or
// neither. Only a coordinate-only transient retry omits both refs: a rebind
// always publishes the replacement ref, and a transient retry revalidated the
// same binding, so it repeats the original ref.
func checkAutoRecovery(recovery *autoRecoveryView) *checkError {
	if recovery == nil {
		return nil
	}
	if !autoRecoveryStrategies[recovery.Strategy] {
		return failf("auto_recovery_strategy_invalid", field("strategy", recovery.Strategy))
	}
	if recovery.Attempts < 1 {
		return failf("auto_recovery_attempts_invalid",
			field("attempts", strconv.Itoa(recovery.Attempts)))
	}
	hasOriginal := recovery.OriginalRef != ""
	hasRecovered := recovery.RecoveredRef != ""
	if hasOriginal != hasRecovered {
		return failf("auto_recovery_refs_incomplete")
	}
	if !hasOriginal {
		if recovery.Strategy != autoRecoveryTransientRetry {
			return failf("auto_recovery_refs_missing", field("strategy", recovery.Strategy))
		}
		return nil
	}
	if !isPublicRef(recovery.OriginalRef) || !isPublicRef(recovery.RecoveredRef) {
		return failf("auto_recovery_ref_invalid")
	}
	if recovery.Strategy == autoRecoveryTransientRetry &&
		recovery.RecoveredRef != recovery.OriginalRef {
		return failf("auto_recovery_transient_refs_differ")
	}
	return nil
}

func checkDiagnosticText(values []string) *checkError {
	if len(values) != 1 {
		return failf("diagnostic_text_count")
	}
	text := strings.TrimSuffix(values[0], "\n")
	if strings.Contains(text, "\r") {
		return failf("diagnostic_text_naked_cr")
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || lines[0] != "diagnostic v1 is_error=true" {
		return failf("diagnostic_text_header_invalid")
	}
	if lines[len(lines)-1] != "end diagnostic" {
		return failf("diagnostic_text_unterminated")
	}
	for _, line := range lines[1 : len(lines)-1] {
		if line != "" && !strings.Contains(line, "=") &&
			!strings.HasPrefix(line, "json v1 ") {
			return failf("diagnostic_text_item_invalid")
		}
	}
	return nil
}

func codeOf(view resultView) string {
	if view.Error == nil {
		return "none"
	}
	return view.Error.Code
}

func treeFormatOf(observation *observationView) string {
	if observation == nil || observation.Tree == nil || observation.Tree.Format == "" {
		return "none"
	}
	return observation.Tree.Format
}

func sessionStateOf(session *sessionView) string {
	if session == nil {
		return "none"
	}
	return session.State
}

// checkObservationQuality asserts the act post observation carries a complete,
// valid quality object. The typed view supplies the values; the decoded object
// proves every member was present, so an omitted samples member can never be
// mistaken for the valid samples=0 fact.
//
// In a text-only projection observation.quality is always omitted. The
// structured channel retains and validates the complete object.
func checkObservationQuality(projected *callResult, observation *observationView) *checkError {
	if observation == nil {
		return failf("post_observation_missing")
	}
	if projected.textOnly {
		return checkObservationQualityOmitted(projected)
	}
	quality := observation.Quality
	if quality == nil {
		return failf("observation_quality_missing")
	}
	value, found := lookupPath(projected.object, []string{"observation", "quality"})
	qualityObject, isObject := value.(map[string]any)
	if !found || !isObject {
		return failf("observation_quality_invalid")
	}
	for _, member := range []string{"state", "stop_reason", "samples"} {
		if _, present := qualityObject[member]; !present {
			return failf("observation_quality_member_missing", field("member", member))
		}
	}
	if !qualityStates[quality.State] {
		return failf("observation_quality_state_invalid", field("state", quality.State))
	}
	if quality.Samples < 0 {
		return failf("observation_quality_samples_invalid",
			field("samples", strconv.Itoa(quality.Samples)))
	}
	if !qualityStopReasons[quality.StopReason] {
		return failf("observation_quality_stop_reason_invalid",
			field("stop_reason", quality.StopReason))
	}
	return nil
}

// observationQualityToken returns the single quality= token of the obs line, or
// "" when the line carries none.
func observationQualityToken(projected *callResult) (string, *checkError) {
	for _, text := range projected.texts {
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			if !strings.HasPrefix(line, "obs ") && line != "obs" {
				continue
			}
			for _, token := range strings.Fields(line) {
				if strings.HasPrefix(token, "quality=") {
					return strings.TrimPrefix(token, "quality="), nil
				}
			}
			return "", nil
		}
	}
	return "", failf("text_obs_line_missing")
}

// checkObservationQualityOmitted proves the text projection carries neither the
// quality object nor its readable token.
func checkObservationQualityOmitted(projected *callResult) *checkError {
	if _, present := lookupPath(projected.object, []string{"observation", "quality"}); present {
		return failf("observation_quality_unexpected")
	}
	token, failure := observationQualityToken(projected)
	if failure != nil {
		return failure
	}
	if token != "" {
		return failf("observation_quality_unexpected")
	}
	return nil
}

// checkScrollEffect validates the structured effect fact and proves a text
// projection omits it. Every structured scroll result must carry a valid state;
// no structured non-scroll result may carry the member, even as explicit null.
func checkScrollEffect(projected *callResult, scroll bool) *checkError {
	if projected.textOnly {
		if _, present := lookupPath(projected.object, []string{"effect"}); present {
			return failf("text_effect_unexpected")
		}
		for _, text := range projected.texts {
			for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
				if strings.HasPrefix(line, "effect=") || strings.HasPrefix(line, "effect.") {
					return failf("text_effect_unexpected")
				}
			}
		}
		return nil
	}
	if !scroll {
		if projected.view.Effect != nil {
			return failf("effect_on_non_scroll")
		}
		if projected.object != nil {
			if _, present := projected.object["effect"]; present {
				return failf("effect_on_non_scroll")
			}
		}
		return nil
	}
	if projected.view.Effect == nil {
		return failf("scroll_effect_missing")
	}
	if !effectStates[projected.view.Effect.State] {
		return failf("scroll_effect_state_invalid", field("state", projected.view.Effect.State))
	}
	return nil
}

// validReference accepts the public P and O identifiers: one prefix byte and
// at most 63 further ASCII letters, digits, hyphen or underscore.
func validReference(value string, prefix byte) bool {
	if len(value) < 2 || len(value) > 64 || value[0] != prefix {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-', character == '_':
		default:
			return false
		}
	}
	return true
}

const (
	// imageMaxLongSide is the frozen output long-edge bound of the image policy.
	// Every screenshot the probe accepts must satisfy it; 461x1024 is the
	// expected 1080x2400 result and must never be judged by the retired 1200
	// bound.
	imageMaxLongSide = 1024
)

// checkImageLongSide rejects an encoded image whose longest edge exceeds the
// single output bound. It deliberately checks the longest edge only, so a
// portrait 461x1024 frame is valid.
func checkImageLongSide(meta *imageView) *checkError {
	if meta.Width > imageMaxLongSide || meta.Height > imageMaxLongSide {
		return failf("image_long_side_exceeds_limit",
			field("width", strconv.Itoa(meta.Width)), field("height", strconv.Itoa(meta.Height)))
	}
	return nil
}

// expectedImageSize reproduces the proportional, never-upscaling output size of
// one screenshot so a snap can prove the encoded 461x1024 geometry instead of
// only accepting any positive size.
func expectedImageSize(width, height, maxLongSide int) (int, int) {
	longSide := width
	if height > longSide {
		longSide = height
	}
	scale := 1.0
	if longSide > maxLongSide {
		scale = float64(maxLongSide) / float64(longSide)
	}
	dstW := int(float64(width)*scale + 0.5)
	dstH := int(float64(height)*scale + 0.5)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	return dstW, dstH
}

// jpegMagic reports whether the bytes start with a JPEG SOI and marker.
func jpegMagic(data []byte) bool {
	return len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF
}

// imageBytes returns the decoded image bytes of the first ImageContent. The
// SDK decodes the base64 carrier into Data; a still-encoded carrier is
// accepted defensively.
func imageBytes(projected *callResult, observation *observationView) ([]byte, *checkError) {
	if len(projected.images) == 0 {
		fields := []string{}
		if observation != nil && observation.Image != nil && observation.Image.Error != nil {
			fields = append(fields, field("image_error", observation.Image.Error.Code))
		}
		return nil, failf("image_content_missing", fields...)
	}
	image := projected.images[0]
	if image.MIMEType != imageJPEG {
		return nil, failf("image_mime_unexpected", field("mime", image.MIMEType))
	}
	raw := image.Data
	if len(raw) == 0 {
		return nil, failf("image_data_empty")
	}
	if !jpegMagic(raw) {
		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			return nil, failf("image_not_jpeg")
		}
		raw = decoded
	}
	if !jpegMagic(raw) {
		return nil, failf("image_not_jpeg")
	}
	if len(raw) <= 1024 {
		return nil, failf("image_too_small", field("image_bytes", strconv.Itoa(len(raw))))
	}
	if observation == nil || observation.Image == nil {
		return nil, failf("image_metadata_missing")
	}
	meta := observation.Image
	if meta.Width <= 0 || meta.Height <= 0 {
		return nil, failf("image_metadata_dimensions",
			field("width", strconv.Itoa(meta.Width)), field("height", strconv.Itoa(meta.Height)))
	}
	if failure := checkImageLongSide(meta); failure != nil {
		return nil, failure
	}
	if meta.MediaType != imageJPEG {
		return nil, failf("image_metadata_mime", field("media_type", meta.MediaType))
	}
	if meta.CapturedAt == "" {
		return nil, failf("image_metadata_missing_captured_at")
	}
	return raw, nil
}

// snapImageBytes returns the one decoded JPEG of a snap success after checking
// the top-level metadata contract: exactly one ImageContent, image/jpeg, the
// JPEG signature, a positive source size, one valid capture time, the output
// pixel size, read_only=true and the admission display snapshot.
func snapImageBytes(projected *callResult, meta *imageView) ([]byte, *checkError) {
	if len(projected.images) != 1 {
		return nil, failf("image_content_count", field("count", strconv.Itoa(len(projected.images))))
	}
	image := projected.images[0]
	if image.MIMEType != imageJPEG {
		return nil, failf("image_mime_unexpected", field("mime", image.MIMEType))
	}
	raw := image.Data
	if len(raw) == 0 {
		return nil, failf("image_data_empty")
	}
	if !jpegMagic(raw) {
		return nil, failf("image_not_jpeg")
	}
	if len(raw) <= 1024 {
		return nil, failf("image_too_small", field("image_bytes", strconv.Itoa(len(raw))))
	}
	if meta == nil {
		return nil, failf("image_metadata_missing")
	}
	if meta.Width <= 0 || meta.Height <= 0 {
		return nil, failf("image_metadata_dimensions",
			field("width", strconv.Itoa(meta.Width)), field("height", strconv.Itoa(meta.Height)))
	}
	if failure := checkImageLongSide(meta); failure != nil {
		return nil, failure
	}
	if meta.MediaType != imageJPEG {
		return nil, failf("image_metadata_mime", field("media_type", meta.MediaType))
	}
	if meta.CapturedAt == "" {
		return nil, failf("image_metadata_missing_captured_at")
	}
	if !meta.ReadOnly {
		return nil, failf("image_metadata_not_read_only")
	}
	if meta.Screen == nil || meta.Screen.Width <= 0 || meta.Screen.Height <= 0 ||
		meta.Screen.Rotation < 0 || meta.Screen.Rotation > 3 {
		return nil, failf("image_metadata_screen_invalid")
	}
	expectedWidth, expectedHeight := expectedImageSize(
		meta.Screen.Width, meta.Screen.Height, imageMaxLongSide)
	if meta.Width != expectedWidth || meta.Height != expectedHeight {
		return nil, failf("image_dimension_mismatch",
			field("expected", strconv.Itoa(expectedWidth)+"x"+strconv.Itoa(expectedHeight)),
			field("actual", strconv.Itoa(meta.Width)+"x"+strconv.Itoa(meta.Height)))
	}
	return raw, nil
}

// treeBlockText returns the readable tree block that starts with headerPrefix.
// The adapter puts the block after a blank line, so the header must start a
// line, not merely appear inside a label.
func treeBlockText(projected *callResult, headerPrefix string) (string, *checkError) {
	end := "end inspect"
	reason := "text_inspect_block_missing"
	if strings.HasPrefix(headerPrefix, "tree v5 ") {
		end = "end tree"
		reason = "text_tree_block_missing"
	}
	return resultBlockText(projected, headerPrefix, end, reason)
}

func resultBlockText(projected *callResult, headerPrefix, end, reason string) (string, *checkError) {
	for _, text := range projected.texts {
		lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		for start, line := range lines {
			if !strings.HasPrefix(line, headerPrefix) {
				continue
			}
			for finish := start + 1; finish < len(lines); finish++ {
				if lines[finish] == end {
					return strings.Join(lines[start:finish+1], "\n") + "\n", nil
				}
			}
			return "", failf("text_block_unterminated")
		}
	}
	return "", failf(reason)
}

// checkNoImageCarrier asserts the text projection and structuredContent carry
// neither the base64 member nor the exact base64 payload of this image.
func checkNoImageCarrier(projected *callResult, raw []byte) *checkError {
	payload := base64.StdEncoding.EncodeToString(raw)
	for _, text := range projected.texts {
		if strings.Contains(text, "base64") {
			return failf("text_carries_base64")
		}
		if strings.Contains(text, payload) {
			return failf("text_carries_image_bytes")
		}
	}
	if len(projected.structured) > 0 {
		if bytes.Contains(projected.structured, []byte(`"base64"`)) {
			return failf("structured_carries_base64")
		}
		if bytes.Contains(projected.structured, []byte(payload)) {
			return failf("structured_carries_image_bytes")
		}
	}
	return nil
}
