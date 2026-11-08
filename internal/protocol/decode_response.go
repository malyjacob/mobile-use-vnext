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
// A member whose wire shape always carries a value (for example Screen.width
// or AppsView.items) is required. Result is a branch union: its members appear
// only when known, so none of them is required by the decoder.

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
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%s must be a JSON object", what)
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
		Screen          *Screen          `json:"screen"`
		Foreground      *Foreground      `json:"foreground"`
		Tree            *TreeValue       `json:"tree"`
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
		Apps                *AppsView                `json:"apps"`
		App                 *AppView                 `json:"app"`
		Shell               *ShellView               `json:"shell"`
		Notification        *NotificationView        `json:"notification"`
		Diagnosis           *DiagnosisView           `json:"diagnosis"`
		Cleanup             *Cleanup                 `json:"cleanup"`
		Error               *Error                   `json:"error"`
		Next                member[string]           `json:"next"`
	}
	if err := readObject(dec, "result", &wire); err != nil {
		return err
	}
	*r = Result{
		OperationID:         wire.OperationID.Value,
		OperationState:      wire.OperationState.Value,
		Outcome:             wire.Outcome.Value,
		Phase:               wire.Phase.Value,
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
		Apps:                wire.Apps,
		App:                 wire.App,
		Shell:               wire.Shell,
		Notification:        wire.Notification,
		Diagnosis:           wire.Diagnosis,
		Cleanup:             wire.Cleanup,
		Error:               wire.Error,
		Next:                wire.Next.Value,
	}
	return nil
}

// UnmarshalJSONFrom decodes the historical inspection material.
func (v *InspectionView) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		ObservationID member[string]            `json:"observation_id"`
		Origin        member[string]            `json:"origin"`
		Revision      member[int]               `json:"revision"`
		CapturedAt    member[string]            `json:"captured_at"`
		Scope         member[string]            `json:"scope"`
		Tree          *Tree                     `json:"tree"`
		Root          *Node                     `json:"root"`
		Matches       member[[]InspectionMatch] `json:"matches"`
		Total         member[int]               `json:"total"`
		Truncated     *bool                     `json:"truncated"`
	}
	if err := readObject(dec, "inspection", &wire); err != nil {
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
		Tree:          wire.Tree,
		Root:          wire.Root,
		Total:         wire.Total.Value,
		HasTotal:      wire.Total.Present,
		Truncated:     wire.Truncated,
	}
	if wire.Matches.Present {
		v.Matches = wire.Matches.Value
	}
	return nil
}

// UnmarshalJSONFrom decodes one matched subtree and its ancestor ref path.
func (m *InspectionMatch) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Node *Node            `json:"node"`
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

// UnmarshalJSONFrom decodes the apps discovery view.
func (a *AppsView) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Items      member[[]AppEntry] `json:"items"`
		Total      member[int]        `json:"total"`
		Truncated  member[bool]       `json:"truncated"`
		CapturedAt member[string]     `json:"captured_at"`
	}
	if err := readObject(dec, "apps", &wire); err != nil {
		return err
	}
	items, err := requireResponseMember(wire.Items, "apps.items")
	if err != nil {
		return err
	}
	total, err := requireResponseMember(wire.Total, "apps.total")
	if err != nil {
		return err
	}
	truncated, err := requireResponseMember(wire.Truncated, "apps.truncated")
	if err != nil {
		return err
	}
	capturedAt, err := requireResponseMember(wire.CapturedAt, "apps.captured_at")
	if err != nil {
		return err
	}
	*a = AppsView{Items: items, Total: total, Truncated: truncated, CapturedAt: capturedAt}
	return nil
}

// UnmarshalJSONFrom decodes one discovered launchable application.
func (e *AppEntry) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Name    member[string] `json:"name"`
		Package member[string] `json:"package"`
	}
	if err := readObject(dec, "app entry", &wire); err != nil {
		return err
	}
	name, err := requireResponseMember(wire.Name, "app entry name")
	if err != nil {
		return err
	}
	pkg, err := requireResponseMember(wire.Package, "app entry package")
	if err != nil {
		return err
	}
	*e = AppEntry{Name: name, Package: pkg}
	return nil
}

// UnmarshalJSONFrom decodes one open_app action fact.
func (a *AppView) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Package        member[string] `json:"package"`
		TaskID         member[int]    `json:"task_id"`
		DisplayID      member[int]    `json:"display_id"`
		Reused         member[bool]   `json:"reused"`
		StatePreserved *bool          `json:"state_preserved"`
	}
	if err := readObject(dec, "app", &wire); err != nil {
		return err
	}
	pkg, err := requireResponseMember(wire.Package, "app.package")
	if err != nil {
		return err
	}
	taskID, err := requireResponseMember(wire.TaskID, "app.task_id")
	if err != nil {
		return err
	}
	displayID, err := requireResponseMember(wire.DisplayID, "app.display_id")
	if err != nil {
		return err
	}
	reused, err := requireResponseMember(wire.Reused, "app.reused")
	if err != nil {
		return err
	}
	*a = AppView{
		Package:        pkg,
		TaskID:         taskID,
		DisplayID:      displayID,
		Reused:         reused,
		StatePreserved: wire.StatePreserved,
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
