package protocol

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

// This file decodes the public response objects written by the custom
// MarshalJSONTo methods in responses.go and surface.go. The wire structs below
// mirror only members the service can emit; an absent member never becomes a
// manufactured value, and explicit null is rejected for plain scalar, array
// and map members. Unknown object members are ignored so a newer service can
// add fields without making an older client unable to read a response. The
// existing Node, Bounds, Tree, Delta and TreeValue decoders keep their own
// stricter member checks and are reached through normal json/v2 decoding.
//
// A member whose wire shape always carries a value (for example Screen.width)
// is required. Result is a branch union: its members appear only when known, so
// none of them is required by the decoder.

// member captures one JSON object member and rejects explicit null. Present
// distinguishes an absent member from an explicit zero value.
type member[T any] struct {
	Present bool
	Value   T
}

// UnmarshalJSONFrom records the member. Null is not a valid scalar, array or
// map value on this wire shape, so it is an error instead of a silent zero.
func (m *member[T]) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	if isJSONNull(raw) {
		return fmt.Errorf("null is not allowed for this member")
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	m.Present = true
	m.Value = value
	return nil
}

// nullableMember also accepts explicit null. It is used only where the wire
// contract permits null itself (Foreground.task_id and Result.session).
type nullableMember[T any] struct {
	Present bool
	Value   T
}

// UnmarshalJSONFrom records presence and decodes a non-null value.
func (m *nullableMember[T]) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	m.Present = true
	if isJSONNull(raw) {
		return nil
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	m.Value = value
	return nil
}

// required returns a member value or reports that the member is absent.
func requireResponseMember[T any](m member[T], name string) (T, error) {
	if !m.Present {
		var zero T
		return zero, fmt.Errorf("response member %q is required", name)
	}
	return m.Value, nil
}

// readObject reads exactly one JSON object into dst. json/v2 accepts a
// top-level null for a struct destination, but every public response object
// is an object, so null must stay an error here.
func readObject(dec *jsontext.Decoder, what string, dst any) error {
	raw, err := readObjectValue(dec, what)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func readObjectValue(dec *jsontext.Decoder, what string) (jsontext.Value, error) {
	raw, err := dec.ReadValue()
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s must be a JSON object", what)
	}
	return raw, nil
}

// readObjectWithoutMember rejects one removed top-level member while preserving
// the forward-compatibility rule for every other unknown member.
func readObjectWithoutMember(dec *jsontext.Decoder, what, member string, dst any) error {
	raw, err := readObjectValue(dec, what)
	if err != nil {
		return err
	}
	var members map[string]jsontext.Value
	if err := json.Unmarshal(raw, &members); err != nil {
		return err
	}
	if _, present := members[member]; present {
		return fmt.Errorf("%s must not declare %q", what, member)
	}
	return json.Unmarshal(raw, dst)
}

func isJSONNull(raw jsontext.Value) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// UnmarshalJSONFrom decodes the public controlled error object.
func (e *Error) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Code    member[string]         `json:"code"`
		Message member[string]         `json:"message"`
		Details member[map[string]any] `json:"details"`
	}
	if err := readObject(dec, "error", &wire); err != nil {
		return err
	}
	code, err := requireResponseMember(wire.Code, "error.code")
	if err != nil {
		return err
	}
	message, err := requireResponseMember(wire.Message, "error.message")
	if err != nil {
		return err
	}
	*e = Error{Code: code, Message: message}
	if wire.Details.Present {
		e.Details = wire.Details.Value
	}
	return nil
}

// UnmarshalJSONFrom decodes the target display description.
func (s *Screen) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Width    member[int] `json:"width"`
		Height   member[int] `json:"height"`
		Rotation member[int] `json:"rotation"`
	}
	if err := readObject(dec, "screen", &wire); err != nil {
		return err
	}
	width, err := requireResponseMember(wire.Width, "screen.width")
	if err != nil {
		return err
	}
	height, err := requireResponseMember(wire.Height, "screen.height")
	if err != nil {
		return err
	}
	rotation, err := requireResponseMember(wire.Rotation, "screen.rotation")
	if err != nil {
		return err
	}
	*s = Screen{Width: width, Height: height, Rotation: rotation}
	return nil
}

// UnmarshalJSONFrom decodes the foreground branch. HasTask records whether
// task_id was present at all: the present branch always carries the member,
// and an explicit null means the task identity was not obtained.
func (f *Foreground) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		State   member[string]       `json:"state"`
		Package member[string]       `json:"package"`
		TaskID  nullableMember[*int] `json:"task_id"`
		Reason  member[string]       `json:"reason"`
	}
	if err := readObject(dec, "foreground", &wire); err != nil {
		return err
	}
	state, err := requireResponseMember(wire.State, "foreground.state")
	if err != nil {
		return err
	}
	*f = Foreground{
		State:   state,
		Package: wire.Package.Value,
		TaskID:  wire.TaskID.Value,
		HasTask: wire.TaskID.Present,
		Reason:  wire.Reason.Value,
	}
	return nil
}

// UnmarshalJSONFrom decodes observation image metadata, including the
// transport-only base64 carrier.
func (m *ImageMeta) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		CapturedAt member[string] `json:"captured_at"`
		Width      member[int]    `json:"width"`
		Height     member[int]    `json:"height"`
		MediaType  member[string] `json:"media_type"`
		ReadOnly   member[bool]   `json:"read_only"`
		Screen     *Screen        `json:"screen"`
		Error      *Error         `json:"error"`
		Base64     member[string] `json:"base64"`
	}
	if err := readObject(dec, "image", &wire); err != nil {
		return err
	}
	*m = ImageMeta{
		CapturedAt: wire.CapturedAt.Value,
		Width:      wire.Width.Value,
		Height:     wire.Height.Value,
		MediaType:  wire.MediaType.Value,
		ReadOnly:   wire.ReadOnly.Value,
		Screen:     wire.Screen,
		Error:      wire.Error,
		Base64:     wire.Base64.Value,
	}
	return nil
}

// UnmarshalJSONFrom decodes the observation quality object. All three members
// are required; explicit null for the object or any member is an error, an
// unknown enum value is an error, and samples=0 is a valid explicit fact.
func (q *Quality) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		State      member[string] `json:"state"`
		Samples    member[int]    `json:"samples"`
		StopReason member[string] `json:"stop_reason"`
	}
	if err := readObject(dec, "quality", &wire); err != nil {
		return err
	}
	state, err := requireResponseMember(wire.State, "quality.state")
	if err != nil {
		return err
	}
	switch state {
	case QualityStable, QualityUnstable, QualityUnknown:
	default:
		return fmt.Errorf("invalid quality.state")
	}
	samples, err := requireResponseMember(wire.Samples, "quality.samples")
	if err != nil {
		return err
	}
	if samples < 0 {
		return fmt.Errorf("quality.samples must not be negative")
	}
	stopReason, err := requireResponseMember(wire.StopReason, "quality.stop_reason")
	if err != nil {
		return err
	}
	switch stopReason {
	case QualityStopStable, QualityStopMaxSamples, QualityStopBudget,
		QualityStopUnreadable, QualityStopNotSampled,
		QualityStopCondition, QualityStopTimeout:
	default:
		return fmt.Errorf("invalid quality.stop_reason")
	}
	*q = Quality{State: state, Samples: samples, StopReason: stopReason}
	return nil
}

// UnmarshalJSONFrom decodes the bounded condition-wait outcome. trigger and
// elapsed_ms are required and enum/number bound; matched is optional, but when
// present it must be a non-empty array of closed-vocabulary entries, so an
// explicit null or an empty list is rejected instead of being folded into
// "no match".
func (w *WaitResult) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Trigger   member[string]           `json:"trigger"`
		Matched   nullableMember[[]string] `json:"matched"`
		ElapsedMS member[int64]            `json:"elapsed_ms"`
	}
	if err := readObject(dec, "wait", &wire); err != nil {
		return err
	}
	trigger, err := requireResponseMember(wire.Trigger, "wait.trigger")
	if err != nil {
		return err
	}
	switch trigger {
	case WaitTriggerQuery, WaitTriggerAbsentQuery, WaitTriggerStable, WaitTriggerTimeout:
	default:
		return fmt.Errorf("invalid wait.trigger")
	}
	elapsed, err := requireResponseMember(wire.ElapsedMS, "wait.elapsed_ms")
	if err != nil {
		return err
	}
	if elapsed < 0 {
		return fmt.Errorf("wait.elapsed_ms must not be negative")
	}
	var matched []string
	if wire.Matched.Present {
		if len(wire.Matched.Value) == 0 {
			return fmt.Errorf("wait.matched must be a non-empty array when present")
		}
		matched = append(matched, wire.Matched.Value...)
		for _, item := range matched {
			switch item {
			case WaitTriggerQuery, WaitTriggerAbsentQuery, WaitTriggerTimeout:
			default:
				return fmt.Errorf("invalid wait.matched entry")
			}
		}
	}
	*w = WaitResult{Trigger: trigger, Matched: matched, ElapsedMS: elapsed}
	return nil
}

// UnmarshalJSONFrom decodes the bounded scroll-effect object. state is
// required and enum-bound; explicit null for the object or member is an error.
func (e *Effect) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		State member[string] `json:"state"`
	}
	if err := readObject(dec, "effect", &wire); err != nil {
		return err
	}
	state, err := requireResponseMember(wire.State, "effect.state")
	if err != nil {
		return err
	}
	switch state {
	case EffectObserved, EffectNotObserved, EffectUnknown:
	default:
		return fmt.Errorf("invalid effect.state")
	}
	*e = Effect{State: state}
	return nil
}

// UnmarshalJSONFrom decodes the single observation shape used by every
// valid, partial and failed branch.
func (o *Observation) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Status          member[string]   `json:"status"`
		ID              member[string]   `json:"id"`
		Origin          member[string]   `json:"origin"`
		Revision        member[int]      `json:"revision"`
		CapturedAt      member[string]   `json:"captured_at"`
		ActionableUntil member[string]   `json:"actionable_until"`
		Quality         member[*Quality] `json:"quality"`
		Screen          *Screen          `json:"screen"`
		Foreground      *Foreground      `json:"foreground"`
		Tree            *ReadTreeValue   `json:"tree"`
		Missing         member[[]string] `json:"missing"`
		Image           *ImageMeta       `json:"image"`
		Error           *Error           `json:"error"`
	}
	if err := readObject(dec, "observation", &wire); err != nil {
		return err
	}
	status, err := requireResponseMember(wire.Status, "observation.status")
	if err != nil {
		return err
	}
	*o = Observation{
		Status:          status,
		ID:              wire.ID.Value,
		Origin:          wire.Origin.Value,
		Revision:        wire.Revision.Value,
		CapturedAt:      wire.CapturedAt.Value,
		ActionableUntil: wire.ActionableUntil.Value,
		Quality:         wire.Quality.Value,
		Screen:          wire.Screen,
		Foreground:      wire.Foreground,
		Tree:            wire.Tree,
		Image:           wire.Image,
		Error:           wire.Error,
	}
	if wire.Missing.Present {
		o.Missing = wire.Missing.Value
	}
	return nil
}

// UnmarshalJSONFrom decodes the public session projection.
func (s *Session) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Origin    member[string] `json:"origin"`
		State     member[string] `json:"state"`
		Mode      member[string] `json:"mode"`
		Agent     member[string] `json:"agent"`
		StartedAt member[string] `json:"started_at"`
		ExpiresAt member[string] `json:"expires_at"`
		EndedAt   member[string] `json:"ended_at"`
		Reason    member[string] `json:"reason"`
	}
	if err := readObject(dec, "session", &wire); err != nil {
		return err
	}
	origin, err := requireResponseMember(wire.Origin, "session.origin")
	if err != nil {
		return err
	}
	state, err := requireResponseMember(wire.State, "session.state")
	if err != nil {
		return err
	}
	mode, err := requireResponseMember(wire.Mode, "session.mode")
	if err != nil {
		return err
	}
	*s = Session{
		Origin:    origin,
		State:     state,
		Mode:      mode,
		Agent:     wire.Agent.Value,
		StartedAt: wire.StartedAt.Value,
		ExpiresAt: wire.ExpiresAt.Value,
		EndedAt:   wire.EndedAt.Value,
		Reason:    wire.Reason.Value,
	}
	return nil
}

// UnmarshalJSONFrom decodes the physical-resource closure report.
func (c *Cleanup) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		State     member[string] `json:"state"`
		Execution member[string] `json:"execution"`
		Tasks     member[string] `json:"tasks"`
		Recents   member[string] `json:"recents"`
		Display   member[string] `json:"display"`
		Error     *Error         `json:"error"`
	}
	if err := readObject(dec, "cleanup", &wire); err != nil {
		return err
	}
	*c = Cleanup{
		State:     wire.State.Value,
		Execution: wire.Execution.Value,
		Tasks:     wire.Tasks.Value,
		Recents:   wire.Recents.Value,
		Display:   wire.Display.Value,
		Error:     wire.Error,
	}
	return nil
}

// UnmarshalJSONFrom decodes the operation/tool report. Session presence is
// the wire evidence for IncludeSession, because the boolean has no member of
// its own; an explicit null session is therefore a present, nil session.
func (r *Result) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		OperationID         member[string]           `json:"operation_id"`
		OperationState      member[string]           `json:"operation_state"`
		Outcome             member[string]           `json:"outcome"`
		Phase               member[string]           `json:"phase"`
		Effect              member[*Effect]          `json:"effect"`
		TargetReference     member[string]           `json:"target_reference"`
		RequestClosed       *bool                    `json:"request_closed"`
		BlockingOperationID member[string]           `json:"blocking_operation_id"`
		CanStart            *bool                    `json:"can_start"`
		BlockingCode        member[string]           `json:"blocking_code"`
		StartOperation      member[string]           `json:"start_operation_id"`
		ObservationState    member[string]           `json:"observation_state"`
		InputState          member[string]           `json:"input_state"`
		MessageID           member[string]           `json:"message_id"`
		Saved               *bool                    `json:"saved"`
		Session             nullableMember[*Session] `json:"session"`
		Observation         *Observation             `json:"observation"`
		Inspection          *InspectionView          `json:"inspection"`
		App                 *AppView                 `json:"app"`
		Shell               *ShellView               `json:"shell"`
		Wait                *WaitResult              `json:"wait"`
		Notification        *NotificationView        `json:"notification"`
		Diagnosis           *DiagnosisView           `json:"diagnosis"`
		Cleanup             *Cleanup                 `json:"cleanup"`
		Error               *Error                   `json:"error"`
		Recovery            *Recovery                `json:"recovery"`
	}
	if err := readObject(dec, "result", &wire); err != nil {
		return err
	}
	*r = Result{
		OperationID:         wire.OperationID.Value,
		OperationState:      wire.OperationState.Value,
		Outcome:             wire.Outcome.Value,
		Phase:               wire.Phase.Value,
		Effect:              wire.Effect.Value,
		TargetReference:     wire.TargetReference.Value,
		RequestClosed:       wire.RequestClosed,
		BlockingOperationID: wire.BlockingOperationID.Value,
		CanStart:            wire.CanStart,
		BlockingCode:        wire.BlockingCode.Value,
		StartOperation:      wire.StartOperation.Value,
		ObservationState:    wire.ObservationState.Value,
		InputState:          wire.InputState.Value,
		MessageID:           wire.MessageID.Value,
		Saved:               wire.Saved,
		IncludeSession:      wire.Session.Present,
		Session:             wire.Session.Value,
		Observation:         wire.Observation,
		Inspection:          wire.Inspection,
		App:                 wire.App,
		Shell:               wire.Shell,
		Wait:                wire.Wait,
		Notification:        wire.Notification,
		Diagnosis:           wire.Diagnosis,
		Cleanup:             wire.Cleanup,
		Error:               wire.Error,
		Recovery:            wire.Recovery,
	}
	return nil
}

// UnmarshalJSONFrom decodes the recovery object. Every member is optional at
// decode time: the service always writes entry and reference when the object is
// present, and this decoder keeps unknown future combinations readable instead
// of failing a whole result.
func (r *Recovery) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Entry      member[string] `json:"entry"`
		Reference  member[string] `json:"reference"`
		InputState member[string] `json:"input_state"`
		Cause      member[string] `json:"cause"`
	}
	if err := readObject(dec, "recovery", &wire); err != nil {
		return err
	}
	*r = Recovery{
		Entry:      wire.Entry.Value,
		Reference:  wire.Reference.Value,
		InputState: wire.InputState.Value,
		Cause:      wire.Cause.Value,
	}
	return nil
}

// UnmarshalJSONFrom decodes the historical inspection material. The tree scope
// declares its view in the nested complete tree; the subtree and query scopes
// declare it once on this envelope.
func (v *InspectionView) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		ObservationID member[string]            `json:"observation_id"`
		Origin        member[string]            `json:"origin"`
		Revision      member[int]               `json:"revision"`
		CapturedAt    member[string]            `json:"captured_at"`
		Scope         member[string]            `json:"scope"`
		View          member[string]            `json:"view"`
		Tree          *ReadTree                 `json:"tree"`
		Root          *ReadNode                 `json:"root"`
		Matches       member[[]InspectionMatch] `json:"matches"`
		Total         member[int]               `json:"total"`
		Truncated     *bool                     `json:"truncated"`
	}
	if err := readObjectWithoutMember(dec, "inspection", "defaults", &wire); err != nil {
		return err
	}
	observationID, err := requireResponseMember(wire.ObservationID, "inspection.observation_id")
	if err != nil {
		return err
	}
	origin, err := requireResponseMember(wire.Origin, "inspection.origin")
	if err != nil {
		return err
	}
	revision, err := requireResponseMember(wire.Revision, "inspection.revision")
	if err != nil {
		return err
	}
	capturedAt, err := requireResponseMember(wire.CapturedAt, "inspection.captured_at")
	if err != nil {
		return err
	}
	scope, err := requireResponseMember(wire.Scope, "inspection.scope")
	if err != nil {
		return err
	}
	*v = InspectionView{
		ObservationID: observationID,
		Origin:        origin,
		Revision:      revision,
		CapturedAt:    capturedAt,
		Scope:         scope,
		View:          wire.View.Value,
		Tree:          wire.Tree,
		Root:          wire.Root,
		Total:         wire.Total.Value,
		HasTotal:      wire.Total.Present,
		Truncated:     wire.Truncated,
	}
	switch scope {
	case "tree":
		if wire.View.Present {
			return fmt.Errorf("inspection tree scope declares view in the nested tree")
		}
		if wire.Tree == nil || wire.Tree.View != ViewDetailed {
			return fmt.Errorf("inspection tree scope requires a detailed complete tree")
		}
	case "subtree", "query":
		if !wire.View.Present {
			return fmt.Errorf("inspection %s scope requires view", scope)
		}
		if v.View != ViewDetailed {
			return fmt.Errorf("inspection view must be detailed")
		}
	default:
		return fmt.Errorf("unsupported inspection scope %q", scope)
	}
	if wire.Matches.Present {
		v.Matches = wire.Matches.Value
	}
	return nil
}

// UnmarshalJSONFrom decodes one matched subtree and its ancestor ref path.
func (m *InspectionMatch) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Node *ReadNode        `json:"node"`
		Path member[[]string] `json:"path"`
	}
	if err := readObject(dec, "inspection match", &wire); err != nil {
		return err
	}
	path, err := requireResponseMember(wire.Path, "inspection match path")
	if err != nil {
		return err
	}
	*m = InspectionMatch{Node: wire.Node, Path: path}
	return nil
}

// UnmarshalJSONFrom decodes the kind-discriminated app view. Branch members are
// optional at decode time — the kind decides which are valid — and the decoder
// keeps unknown future members readable instead of failing a whole result.
func (a *AppView) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Kind              member[string] `json:"kind"`
		Items             jsontext.Value `json:"items"`
		Total             member[int]    `json:"total"`
		Truncated         *bool          `json:"truncated"`
		CapturedAt        member[string] `json:"captured_at"`
		DisplayID         *int           `json:"display_id"`
		Foreground        *Foreground    `json:"foreground"`
		Package           member[string] `json:"package"`
		TaskID            *int           `json:"task_id"`
		Ownership         member[string] `json:"ownership"`
		Reused            *bool          `json:"reused"`
		StatePreserved    jsontext.Value `json:"state_preserved"`
		SourceDisplayID   *int           `json:"source_display_id"`
		ForegroundRestore member[string] `json:"foreground_restore"`
		Tasks             jsontext.Value `json:"tasks"`
		Complete          *bool          `json:"complete"`
		SourceTaskID      *int           `json:"source_task_id"`
		TargetPIDs        jsontext.Value `json:"target_pids"`
		KilledPIDs        jsontext.Value `json:"killed_pids"`
		RemainingPIDs     jsontext.Value `json:"remaining_pids"`
		Restarted         jsontext.Value `json:"restarted"`
	}
	if err := readObject(dec, "app", &wire); err != nil {
		return err
	}
	kind, err := requireResponseMember(wire.Kind, "app.kind")
	if err != nil {
		return err
	}
	view := AppView{
		Kind:              kind,
		Total:             wire.Total.Value,
		HasTotal:          wire.Total.Present,
		Truncated:         wire.Truncated,
		CapturedAt:        wire.CapturedAt.Value,
		DisplayID:         wire.DisplayID,
		Foreground:        wire.Foreground,
		Package:           wire.Package.Value,
		TaskID:            wire.TaskID,
		Ownership:         wire.Ownership.Value,
		Reused:            wire.Reused,
		SourceDisplayID:   wire.SourceDisplayID,
		ForegroundRestore: wire.ForegroundRestore.Value,
		Complete:          wire.Complete,
		SourceTaskID:      wire.SourceTaskID,
	}
	if len(wire.StatePreserved) > 0 {
		view.HasStatePreserved = true
		if !isJSONNull(wire.StatePreserved) {
			var value bool
			if err := json.Unmarshal(wire.StatePreserved, &value); err != nil {
				return err
			}
			view.StatePreserved = &value
		}
	}
	if len(wire.Restarted) > 0 {
		view.HasRestarted = true
		if !isJSONNull(wire.Restarted) {
			var value bool
			if err := json.Unmarshal(wire.Restarted, &value); err != nil {
				return err
			}
			view.Restarted = &value
		}
	}
	if err := decodeAppItems(&view, wire.Items); err != nil {
		return err
	}
	if err := decodeAppTasks(&view, wire.Tasks); err != nil {
		return err
	}
	for _, field := range []struct {
		name   string
		raw    jsontext.Value
		target *[]int
		has    *bool
	}{
		{"target_pids", wire.TargetPIDs, &view.TargetPIDs, &view.HasTargetPIDs},
		{"killed_pids", wire.KilledPIDs, &view.KilledPIDs, &view.HasKilledPIDs},
		{"remaining_pids", wire.RemainingPIDs, &view.RemainingPIDs, &view.HasRemainingPIDs},
	} {
		if len(field.raw) == 0 {
			continue
		}
		if isJSONNull(field.raw) {
			return fmt.Errorf("response member %q must not be null", "app."+field.name)
		}
		var ids []int
		if err := json.Unmarshal(field.raw, &ids); err != nil {
			return err
		}
		*field.target = ids
		*field.has = true
	}
	*a = view
	return nil
}

// decodeAppItems decodes the find or list item array, selected by kind.
func decodeAppItems(view *AppView, raw jsontext.Value) error {
	if len(raw) == 0 {
		return nil
	}
	if isJSONNull(raw) {
		return fmt.Errorf("response member %q must not be null", "app.items")
	}
	if view.Kind == AppKindList {
		var items []struct {
			TaskID        member[int]    `json:"task_id"`
			Package       member[string] `json:"package"`
			Ownership     member[string] `json:"ownership"`
			OriginDisplay *int           `json:"origin_display"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		view.TaskItems = make([]AppTaskItem, 0, len(items))
		view.HasItems = true
		for _, item := range items {
			taskID, err := requireResponseMember(item.TaskID, "app.items.task_id")
			if err != nil {
				return err
			}
			pkg, err := requireResponseMember(item.Package, "app.items.package")
			if err != nil {
				return err
			}
			ownership, err := requireResponseMember(item.Ownership, "app.items.ownership")
			if err != nil {
				return err
			}
			view.TaskItems = append(view.TaskItems, AppTaskItem{
				TaskID: taskID, Package: pkg, Ownership: ownership,
				OriginDisplay: item.OriginDisplay,
			})
		}
		return nil
	}
	var items []struct {
		Name    member[string] `json:"name"`
		Package member[string] `json:"package"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	view.Items = make([]AppItem, 0, len(items))
	view.HasItems = true
	for _, item := range items {
		name, err := requireResponseMember(item.Name, "app.items.name")
		if err != nil {
			return err
		}
		pkg, err := requireResponseMember(item.Package, "app.items.package")
		if err != nil {
			return err
		}
		view.Items = append(view.Items, AppItem{Name: name, Package: pkg})
	}
	return nil
}

// decodeAppTasks decodes the close task array.
func decodeAppTasks(view *AppView, raw jsontext.Value) error {
	if len(raw) == 0 {
		return nil
	}
	if isJSONNull(raw) {
		return fmt.Errorf("response member %q must not be null", "app.tasks")
	}
	var items []struct {
		TaskID         member[int]    `json:"task_id"`
		Outcome        member[string] `json:"outcome"`
		RecentsCleaned jsontext.Value `json:"recents_cleaned"`
		Error          *Error         `json:"error"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	view.Tasks = make([]AppCloseTask, 0, len(items))
	view.HasTasks = true
	for _, item := range items {
		taskID, err := requireResponseMember(item.TaskID, "app.tasks.task_id")
		if err != nil {
			return err
		}
		outcome, err := requireResponseMember(item.Outcome, "app.tasks.outcome")
		if err != nil {
			return err
		}
		task := AppCloseTask{TaskID: taskID, Outcome: outcome, Error: item.Error}
		if len(item.RecentsCleaned) > 0 {
			task.HasRecentsCleaned = true
			if isJSONNull(item.RecentsCleaned) {
				task.RecentsCleaned = nil
			} else {
				var cleaned bool
				if err := json.Unmarshal(item.RecentsCleaned, &cleaned); err != nil {
					return err
				}
				task.RecentsCleaned = &cleaned
			}
		}
		view.Tasks = append(view.Tasks, task)
	}
	return nil
}

// UnmarshalJSONFrom decodes the managed shell execution report. exit_code,
// output and truncated are always carried, so presence is required while
// explicit null still means the fact was not obtained.
func (s *ShellView) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Termination member[string]          `json:"termination"`
		ExitCode    nullableMember[*int]    `json:"exit_code"`
		Output      nullableMember[*string] `json:"output"`
		Truncated   nullableMember[*bool]   `json:"truncated"`
		OutputError *Error                  `json:"output_error"`
	}
	if err := readObject(dec, "shell", &wire); err != nil {
		return err
	}
	termination, err := requireResponseMember(wire.Termination, "shell.termination")
	if err != nil {
		return err
	}
	if !wire.ExitCode.Present {
		return fmt.Errorf("response member %q is required", "shell.exit_code")
	}
	if !wire.Output.Present {
		return fmt.Errorf("response member %q is required", "shell.output")
	}
	if !wire.Truncated.Present {
		return fmt.Errorf("response member %q is required", "shell.truncated")
	}
	*s = ShellView{
		Termination: termination,
		ExitCode:    wire.ExitCode.Value,
		Output:      wire.Output.Value,
		Truncated:   wire.Truncated.Value,
		OutputError: wire.OutputError,
	}
	return nil
}

// UnmarshalJSONFrom decodes the notify delivery fact.
func (n *NotificationView) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		State member[string] `json:"state"`
		Error *Error         `json:"error"`
	}
	if err := readObject(dec, "notification", &wire); err != nil {
		return err
	}
	state, err := requireResponseMember(wire.State, "notification.state")
	if err != nil {
		return err
	}
	*n = NotificationView{State: state, Error: wire.Error}
	return nil
}

// UnmarshalJSONFrom decodes the bounded diagnose report.
func (d *DiagnosisView) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Summary   member[string]           `json:"summary"`
		Issues    member[[]DiagnosisIssue] `json:"issues"`
		TimingMS  member[map[string]int64] `json:"timing_ms"`
		Timeline  member[[]DiagnosisEvent] `json:"timeline"`
		Coverage  member[string]           `json:"coverage"`
		Truncated member[bool]             `json:"truncated"`
		Operation member[string]           `json:"operation_id"`
		Outcome   member[string]           `json:"outcome"`
	}
	if err := readObject(dec, "diagnosis", &wire); err != nil {
		return err
	}
	summary, err := requireResponseMember(wire.Summary, "diagnosis.summary")
	if err != nil {
		return err
	}
	issues, err := requireResponseMember(wire.Issues, "diagnosis.issues")
	if err != nil {
		return err
	}
	coverage, err := requireResponseMember(wire.Coverage, "diagnosis.coverage")
	if err != nil {
		return err
	}
	truncated, err := requireResponseMember(wire.Truncated, "diagnosis.truncated")
	if err != nil {
		return err
	}
	*d = DiagnosisView{
		Summary:   summary,
		Issues:    issues,
		TimingMS:  wire.TimingMS.Value,
		Timeline:  wire.Timeline.Value,
		Coverage:  coverage,
		Truncated: truncated,
		Operation: wire.Operation.Value,
		Outcome:   wire.Outcome.Value,
	}
	return nil
}

// UnmarshalJSONFrom decodes one controlled diagnosis issue.
func (i *DiagnosisIssue) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Phase member[string] `json:"phase"`
		Code  member[string] `json:"code"`
	}
	if err := readObject(dec, "diagnosis issue", &wire); err != nil {
		return err
	}
	phase, err := requireResponseMember(wire.Phase, "diagnosis issue phase")
	if err != nil {
		return err
	}
	code, err := requireResponseMember(wire.Code, "diagnosis issue code")
	if err != nil {
		return err
	}
	*i = DiagnosisIssue{Phase: phase, Code: code}
	return nil
}

// UnmarshalJSONFrom decodes one short ordered diagnostic event.
func (e *DiagnosisEvent) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		At    member[string] `json:"at"`
		Phase member[string] `json:"phase"`
		Code  member[string] `json:"code"`
	}
	if err := readObject(dec, "diagnosis event", &wire); err != nil {
		return err
	}
	at, err := requireResponseMember(wire.At, "diagnosis event at")
	if err != nil {
		return err
	}
	phase, err := requireResponseMember(wire.Phase, "diagnosis event phase")
	if err != nil {
		return err
	}
	*e = DiagnosisEvent{At: at, Phase: phase, Code: wire.Code.Value}
	return nil
}

// UnmarshalJSONFrom decodes the unauthenticated process discovery shape.
func (h *Health) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Status            member[string] `json:"status"`
		ContractVersion   member[string] `json:"contract_version"`
		ServiceInstanceID member[string] `json:"service_instance_id"`
		ReleaseVersion    member[string] `json:"release_version"`
	}
	if err := readObject(dec, "health", &wire); err != nil {
		return err
	}
	status, err := requireResponseMember(wire.Status, "health.status")
	if err != nil {
		return err
	}
	contractVersion, err := requireResponseMember(wire.ContractVersion, "health.contract_version")
	if err != nil {
		return err
	}
	serviceInstanceID, err := requireResponseMember(wire.ServiceInstanceID, "health.service_instance_id")
	if err != nil {
		return err
	}
	releaseVersion, err := requireResponseMember(wire.ReleaseVersion, "health.release_version")
	if err != nil {
		return err
	}
	*h = Health{
		Status:            status,
		ContractVersion:   contractVersion,
		ServiceInstanceID: serviceInstanceID,
		ReleaseVersion:    releaseVersion,
	}
	return nil
}
