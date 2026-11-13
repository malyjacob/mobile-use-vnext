package protocol

import (
	"encoding/json/jsontext"
	"fmt"
)

// Screen is the target display size and Android rotation.
type Screen struct {
	Width    int `json:"width"`
	Height   int `json:"height"`
	Rotation int `json:"rotation"`
}

// Foreground is the observed foreground record for the target display.
type Foreground struct {
	State   string
	Package string
	TaskID  *int
	HasTask bool
	Reason  string
}

// MarshalJSONTo writes the foreground branch.
func (f Foreground) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "state"); err != nil {
		return err
	}
	if err := writeString(enc, f.State); err != nil {
		return err
	}
	switch f.State {
	case "present":
		if f.Package != "" {
			if err := writeName(enc, "package"); err != nil {
				return err
			}
			if err := writeString(enc, f.Package); err != nil {
				return err
			}
		}
		if err := writeName(enc, "task_id"); err != nil {
			return err
		}
		if f.TaskID == nil {
			if err := writeNull(enc); err != nil {
				return err
			}
		} else if err := writeInt(enc, *f.TaskID); err != nil {
			return err
		}
	case "unknown":
		if err := writeName(enc, "reason"); err != nil {
			return err
		}
		if err := writeString(enc, f.Reason); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// ImageMeta is the observation image metadata. The bytes travel in the
// transport (the HTTP/SDK base64 carrier / MCP ImageContent), never inside the
// tree text. Base64 is set only by the HTTP boundary and is removed by the MCP
// adapter before it renders text or structured content.
type ImageMeta struct {
	CapturedAt string
	Width      int
	Height     int
	MediaType  string
	ReadOnly   bool
	Screen     *Screen
	Error      *Error
	Base64     string
}

// MarshalJSONTo writes image metadata.
func (m ImageMeta) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if m.CapturedAt != "" {
		if err := writeName(enc, "captured_at"); err != nil {
			return err
		}
		if err := writeString(enc, m.CapturedAt); err != nil {
			return err
		}
	}
	if m.Width > 0 && m.Height > 0 {
		if err := writeName(enc, "width"); err != nil {
			return err
		}
		if err := writeInt(enc, m.Width); err != nil {
			return err
		}
		if err := writeName(enc, "height"); err != nil {
			return err
		}
		if err := writeInt(enc, m.Height); err != nil {
			return err
		}
	}
	if m.MediaType != "" {
		if err := writeName(enc, "media_type"); err != nil {
			return err
		}
		if err := writeString(enc, m.MediaType); err != nil {
			return err
		}
	}
	if m.ReadOnly {
		if err := writeName(enc, "read_only"); err != nil {
			return err
		}
		if err := writeBool(enc, true); err != nil {
			return err
		}
	}
	if m.Screen != nil {
		if err := writeName(enc, "screen"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, m.Screen); err != nil {
			return err
		}
	}
	if m.Error != nil {
		if err := writeName(enc, "error"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, m.Error); err != nil {
			return err
		}
	}
	if m.Base64 != "" {
		// Transport carrier only: the encoded image bytes with the MIME type
		// above. MCP projects them into an ImageContent instead.
		if err := writeName(enc, "base64"); err != nil {
			return err
		}
		if err := writeString(enc, m.Base64); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Quality state values describe whether the published tree converged. They are
// observation material, never input authorization.
const (
	QualityStable   = "stable"
	QualityUnstable = "unstable"
	QualityUnknown  = "unknown"
)

// Quality stop reasons name why settle ended.
const (
	QualityStopStable     = "stable"
	QualityStopMaxSamples = "max_samples"
	QualityStopBudget     = "budget"
	QualityStopUnreadable = "unreadable"
	QualityStopNotSampled = "not_sampled"
)

// Quality is the settle quality of one published observation. All three members
// are explicit facts: samples=0 and stop_reason=not_sampled must be encoded.
type Quality struct {
	State      string
	Samples    int
	StopReason string
}

// MarshalJSONTo writes the quality object in its fixed member order.
func (q *Quality) MarshalJSONTo(enc *jsontext.Encoder) error {
	if q == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "state"); err != nil {
		return err
	}
	if err := writeString(enc, q.State); err != nil {
		return err
	}
	if err := writeName(enc, "samples"); err != nil {
		return err
	}
	if err := writeInt(enc, q.Samples); err != nil {
		return err
	}
	if err := writeName(enc, "stop_reason"); err != nil {
		return err
	}
	if err := writeString(enc, q.StopReason); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Effect state values report whether content displacement was observed. They
// never restate the action outcome and never claim direction or causality.
const (
	EffectObserved    = "observed"
	EffectNotObserved = "not_observed"
	EffectUnknown     = "unknown"
)

// Effect is the bounded scroll-effect fact of one result. It carries one
// member only; coverage, anchors and reasons stay in controlled logs.
type Effect struct {
	State string
}

// MarshalJSONTo writes the effect object.
func (e *Effect) MarshalJSONTo(enc *jsontext.Encoder) error {
	if e == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "state"); err != nil {
		return err
	}
	if err := writeString(enc, e.State); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Observation is the single observation shape for all valid/missing branches.
type Observation struct {
	Status          string
	ID              string
	Origin          string
	Revision        int
	CapturedAt      string
	ActionableUntil string
	Quality         *Quality
	Screen          *Screen
	Foreground      *Foreground
	Tree            *ReadTreeValue
	Missing         []string
	Image           *ImageMeta
	Error           *Error
}

// MarshalJSONTo writes only the fields valid for the observation status.
func (o *Observation) MarshalJSONTo(enc *jsontext.Encoder) error {
	if o == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "status"); err != nil {
		return err
	}
	if err := writeString(enc, o.Status); err != nil {
		return err
	}
	if o.ID != "" {
		if err := writeName(enc, "id"); err != nil {
			return err
		}
		if err := writeString(enc, o.ID); err != nil {
			return err
		}
	}
	if o.Origin != "" {
		if err := writeName(enc, "origin"); err != nil {
			return err
		}
		if err := writeString(enc, o.Origin); err != nil {
			return err
		}
	}
	if o.Revision > 0 {
		if err := writeName(enc, "revision"); err != nil {
			return err
		}
		if err := writeInt(enc, o.Revision); err != nil {
			return err
		}
	}
	if o.CapturedAt != "" {
		if err := writeName(enc, "captured_at"); err != nil {
			return err
		}
		if err := writeString(enc, o.CapturedAt); err != nil {
			return err
		}
	}
	if o.ActionableUntil != "" {
		if err := writeName(enc, "actionable_until"); err != nil {
			return err
		}
		if err := writeString(enc, o.ActionableUntil); err != nil {
			return err
		}
	}
	if o.Quality != nil {
		if err := writeName(enc, "quality"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, o.Quality); err != nil {
			return err
		}
	}
	if o.Screen != nil {
		if err := writeName(enc, "screen"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, o.Screen); err != nil {
			return err
		}
	}
	if o.Foreground != nil {
		if err := writeName(enc, "foreground"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, o.Foreground); err != nil {
			return err
		}
	}
	if o.Tree != nil {
		if err := writeName(enc, "tree"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, o.Tree); err != nil {
			return err
		}
	}
	if len(o.Missing) > 0 {
		if err := writeName(enc, "missing"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, missing := range o.Missing {
			if err := writeString(enc, missing); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	}
	if o.Image != nil {
		if err := writeName(enc, "image"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, o.Image); err != nil {
			return err
		}
	}
	if o.Error != nil {
		if err := writeName(enc, "error"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, o.Error); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Session is the public session projection.
type Session struct {
	Origin    string
	State     string // starting / active / ended
	Mode      string // virtual / primary
	Agent     string
	StartedAt string
	ExpiresAt string
	EndedAt   string
	Reason    string
}

// MarshalJSONTo writes session fields appropriate to its state.
func (s *Session) MarshalJSONTo(enc *jsontext.Encoder) error {
	if s == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "origin"); err != nil {
		return err
	}
	if err := writeString(enc, s.Origin); err != nil {
		return err
	}
	if err := writeName(enc, "state"); err != nil {
		return err
	}
	if err := writeString(enc, s.State); err != nil {
		return err
	}
	if err := writeName(enc, "mode"); err != nil {
		return err
	}
	if err := writeString(enc, s.Mode); err != nil {
		return err
	}
	if s.Agent != "" {
		if err := writeName(enc, "agent"); err != nil {
			return err
		}
		if err := writeString(enc, s.Agent); err != nil {
			return err
		}
	}
	if s.StartedAt != "" {
		if err := writeName(enc, "started_at"); err != nil {
			return err
		}
		if err := writeString(enc, s.StartedAt); err != nil {
			return err
		}
	}
	switch s.State {
	case "starting", "active":
		if s.ExpiresAt != "" {
			if err := writeName(enc, "expires_at"); err != nil {
				return err
			}
			if err := writeString(enc, s.ExpiresAt); err != nil {
				return err
			}
		}
	case "ended":
		if s.EndedAt != "" {
			if err := writeName(enc, "ended_at"); err != nil {
				return err
			}
			if err := writeString(enc, s.EndedAt); err != nil {
				return err
			}
		}
		if s.Reason != "" {
			if err := writeName(enc, "reason"); err != nil {
				return err
			}
			if err := writeString(enc, s.Reason); err != nil {
				return err
			}
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Cleanup values.
const (
	CleanupPending       = "pending"
	CleanupComplete      = "complete"
	CleanupUnconfirmed   = "unconfirmed"
	CleanupConfirmed     = "confirmed"
	CleanupNotApplicable = "not_applicable"
	CleanupNone          = "none"
)

// Cleanup is the physical-resource closure report.
type Cleanup struct {
	State     string
	Execution string
	Tasks     string
	Recents   string
	Display   string
	Error     *Error
}

// MarshalJSONTo writes the cleanup report.
func (c *Cleanup) MarshalJSONTo(enc *jsontext.Encoder) error {
	if c == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"state", c.State}, {"execution", c.Execution}, {"tasks", c.Tasks},
		{"recents", c.Recents}, {"display", c.Display},
	} {
		if field.value == "" {
			continue
		}
		if err := writeName(enc, field.name); err != nil {
			return err
		}
		if err := writeString(enc, field.value); err != nil {
			return err
		}
	}
	if c.Error != nil {
		if err := writeName(enc, "error"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, c.Error); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Recovery is the single "what to do next" projection of one tool result. It
// never changes the action outcome, the observation status, the session state
// or the cleanup facts; it only names the next entry, the reference kind that
// entry takes, the usability of the observation this request already
// referenced, and — only when the result carries no error — why an apparently
// successful or running result still needs recovery.
type Recovery struct {
	Entry      string
	Reference  string
	InputState string
	Cause      string
}

// Entry values. Exactly one names the next request to make.
const (
	RecoveryEntryRetry   = "retry"
	RecoveryEntryObserve = "observe"
	RecoveryEntryStatus  = "status"
	RecoveryEntryEnd     = "end"
	RecoveryEntryUser    = "user"
	RecoveryEntryUpdate  = "update"
)

// Reference values. They name the kind of reference, never an ID.
const (
	RecoveryReferenceP    = "P"
	RecoveryReferenceO    = "O"
	RecoveryReferenceNone = "none"
)

// Input-state values reuse the status(O) vocabulary exactly.
const (
	RecoveryInputEffective = "effective"
	RecoveryInputExpired   = "expired"
	RecoveryInputRevoked   = "revoked"
	RecoveryInputClaimed   = "claimed"
	RecoveryInputRetired   = "retired"
)

// Cause values explain a degraded success or a running result. A cause never
// appears together with an error.
const (
	RecoveryCausePostObservationFailed = "post_observation_failed"
	RecoveryCauseExecutedRevoked       = "executed_revoked"
	RecoveryCauseCleanupPending        = "cleanup_pending"
	RecoveryCauseRunning               = "running"
)

// MarshalJSONTo writes the recovery object in its fixed member order. Entry and
// reference are always present when the object is present; input_state and cause
// only when they name a fact.
func (r *Recovery) MarshalJSONTo(enc *jsontext.Encoder) error {
	if r == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"entry", r.Entry}, {"reference", r.Reference},
		{"input_state", r.InputState}, {"cause", r.Cause},
	} {
		if field.value == "" {
			continue
		}
		if err := writeName(enc, field.name); err != nil {
			return err
		}
		if err := writeString(enc, field.value); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Result is the operation/tool report. Only fields known for this call appear.
type Result struct {
	OperationID         string
	OperationState      string
	Outcome             string
	Phase               string
	Effect              *Effect
	Observation         *Observation
	Inspection          *InspectionView
	App                 *AppView
	Shell               *ShellView
	Notification        *NotificationView
	Diagnosis           *DiagnosisView
	Session             *Session
	IncludeSession      bool
	Cleanup             *Cleanup
	Error               *Error
	Recovery            *Recovery
	TargetReference     string
	RequestClosed       *bool
	BlockingOperationID string
	CanStart            *bool
	BlockingCode        string
	StartOperation      string
	ObservationState    string
	InputState          string
	MessageID           string
	Saved               *bool
}

// MarshalJSONTo writes the result fields in the fixed public order.
func (r *Result) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if r.OperationID != "" {
		if err := writeName(enc, "operation_id"); err != nil {
			return err
		}
		if err := writeString(enc, r.OperationID); err != nil {
			return err
		}
	}
	if r.OperationState != "" {
		if err := writeName(enc, "operation_state"); err != nil {
			return err
		}
		if err := writeString(enc, r.OperationState); err != nil {
			return err
		}
	}
	if r.Outcome != "" {
		if err := writeName(enc, "outcome"); err != nil {
			return err
		}
		if err := writeString(enc, r.Outcome); err != nil {
			return err
		}
	}
	if r.Phase != "" {
		if err := writeName(enc, "phase"); err != nil {
			return err
		}
		if err := writeString(enc, r.Phase); err != nil {
			return err
		}
	}
	if r.Effect != nil {
		if err := writeName(enc, "effect"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Effect); err != nil {
			return err
		}
	}
	if r.TargetReference != "" {
		if err := writeName(enc, "target_reference"); err != nil {
			return err
		}
		if err := writeString(enc, r.TargetReference); err != nil {
			return err
		}
	}
	if r.RequestClosed != nil {
		if err := writeName(enc, "request_closed"); err != nil {
			return err
		}
		if err := writeBool(enc, *r.RequestClosed); err != nil {
			return err
		}
	}
	if r.BlockingOperationID != "" {
		if err := writeName(enc, "blocking_operation_id"); err != nil {
			return err
		}
		if err := writeString(enc, r.BlockingOperationID); err != nil {
			return err
		}
	}
	if r.CanStart != nil {
		if err := writeName(enc, "can_start"); err != nil {
			return err
		}
		if err := writeBool(enc, *r.CanStart); err != nil {
			return err
		}
	}
	if r.BlockingCode != "" {
		if err := writeName(enc, "blocking_code"); err != nil {
			return err
		}
		if err := writeString(enc, r.BlockingCode); err != nil {
			return err
		}
	}
	if r.StartOperation != "" {
		if err := writeName(enc, "start_operation_id"); err != nil {
			return err
		}
		if err := writeString(enc, r.StartOperation); err != nil {
			return err
		}
	}
	if r.ObservationState != "" {
		if err := writeName(enc, "observation_state"); err != nil {
			return err
		}
		if err := writeString(enc, r.ObservationState); err != nil {
			return err
		}
	}
	if r.InputState != "" {
		if err := writeName(enc, "input_state"); err != nil {
			return err
		}
		if err := writeString(enc, r.InputState); err != nil {
			return err
		}
	}
	if r.MessageID != "" {
		if err := writeName(enc, "message_id"); err != nil {
			return err
		}
		if err := writeString(enc, r.MessageID); err != nil {
			return err
		}
	}
	if r.Saved != nil {
		if err := writeName(enc, "saved"); err != nil {
			return err
		}
		if err := writeBool(enc, *r.Saved); err != nil {
			return err
		}
	}
	if r.IncludeSession {
		if err := writeName(enc, "session"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Session); err != nil {
			return err
		}
	}
	if r.Observation != nil {
		if err := writeName(enc, "observation"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Observation); err != nil {
			return err
		}
	}
	if r.Inspection != nil {
		if err := writeName(enc, "inspection"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Inspection); err != nil {
			return err
		}
	}
	if r.App != nil {
		if err := writeName(enc, "app"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.App); err != nil {
			return err
		}
	}
	if r.Shell != nil {
		if err := writeName(enc, "shell"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Shell); err != nil {
			return err
		}
	}
	if r.Notification != nil {
		if err := writeName(enc, "notification"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Notification); err != nil {
			return err
		}
	}
	if r.Diagnosis != nil {
		if err := writeName(enc, "diagnosis"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Diagnosis); err != nil {
			return err
		}
	}
	if r.Cleanup != nil {
		if err := writeName(enc, "cleanup"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Cleanup); err != nil {
			return err
		}
	}
	if r.Error != nil {
		if err := writeName(enc, "error"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Error); err != nil {
			return err
		}
	}
	if r.Recovery != nil {
		if err := writeName(enc, "recovery"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, r.Recovery); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// ErrorEnvelope is the HTTP error body.
type ErrorEnvelope struct {
	Error *Result `json:"error"`
}

// Health is the unauthenticated process discovery shape. status=ok only means
// the process answers; it is not device cleanliness or readiness.
type Health struct {
	Status            string `json:"status"`
	ContractVersion   string `json:"contract_version"`
	ServiceInstanceID string `json:"service_instance_id"`
	ReleaseVersion    string `json:"release_version"`
}

// AppDisplay is the control App's report of one physical/logical display.
// The density member uses the same `density_dpi` name as display commands so
// the App's existing descriptor serialization stays a single schema.
type AppDisplay struct {
	DisplayID  int    `json:"display_id"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Density    int    `json:"density_dpi"`
	Rotation   int    `json:"rotation"`
	Generation string `json:"generation"`
	IMEHide    bool   `json:"ime_hide"`
}

// Validate checks a reported display identity.
func (d AppDisplay) Validate() error {
	if d.DisplayID < 0 || d.Width <= 0 || d.Height <= 0 || d.Density <= 0 {
		return fmt.Errorf("invalid display descriptor")
	}
	if d.Rotation < 0 || d.Rotation > 3 {
		return fmt.Errorf("invalid display rotation")
	}
	if d.Generation == "" {
		return fmt.Errorf("display generation is required")
	}
	return nil
}

// AppCommandResult acknowledges one delivered display command.
type AppCommandResult struct {
	CommandID string  `json:"command_id"`
	OK        bool    `json:"ok"`
	ErrorCode *string `json:"error_code"`
}

// AppStopPending is the local stop intent awaiting service acceptance.
type AppStopPending struct {
	Seq    int64  `json:"seq"`
	Reason string `json:"reason"`
}

// AppMessage is one saved Agent message delivered through the control sync.
// The body travels only on this channel: it is never logged, written to the
// recovery ledger or placed in an error.
type AppMessage struct {
	ID        string `json:"id"`
	Seq       int64  `json:"seq"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

// AppSyncRequest is the control App heartbeat.
type AppSyncRequest struct {
	ContractVersion      string             `json:"contract_version"`
	AppInstanceID        string             `json:"app_instance_id"`
	AppVersion           string             `json:"app_version"`
	SelectedMode         string             `json:"selected_mode"`
	ModeGeneration       string             `json:"mode_generation"`
	ModeReady            bool               `json:"mode_ready"`
	StopReady            bool               `json:"stop_ready"`
	NotificationsEnabled bool               `json:"notifications_enabled"`
	BackgroundRunning    bool               `json:"background_running"`
	PowerInteractive     *bool              `json:"power_interactive"`
	KeyguardLocked       *bool              `json:"keyguard_locked"`
	PrimaryDisplay       *AppDisplay        `json:"primary_display"`
	Display              *AppDisplay        `json:"display"`
	CommandResults       []AppCommandResult `json:"command_results"`
	StopPending          *AppStopPending    `json:"stop_pending"`
	// MessageAcks releases messages the App has durably stored. Unknown or
	// already released ids are ignored.
	MessageAcks []string `json:"message_acks"`
}

// AppCommand is one display lifecycle command delivered in a sync response.
type AppCommand struct {
	ID                    string
	Kind                  string // create_display / close_display / cancel_create
	Width                 int
	Height                int
	DensityDPI            int
	TargetAppInstanceID   string
	TargetDisplayID       *int
	TargetGeneration      string
	TargetCreateCommandID string `json:"target_create_command_id,omitempty"`
}

// MarshalJSONTo writes only the fields belonging to the command kind.
func (c AppCommand) MarshalJSONTo(enc *jsontext.Encoder) error {
	if c.Kind != "cancel_create" && c.TargetCreateCommandID != "" {
		return fmt.Errorf("target_create_command_id is only valid for cancel_create")
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "id"); err != nil {
		return err
	}
	if err := writeString(enc, c.ID); err != nil {
		return err
	}
	if err := writeName(enc, "kind"); err != nil {
		return err
	}
	if err := writeString(enc, c.Kind); err != nil {
		return err
	}
	switch c.Kind {
	case "create_display":
		for _, field := range []struct {
			name  string
			value int
		}{{"width", c.Width}, {"height", c.Height}, {"density_dpi", c.DensityDPI}} {
			if err := writeName(enc, field.name); err != nil {
				return err
			}
			if err := writeInt(enc, field.value); err != nil {
				return err
			}
		}
	case "close_display":
		if err := writeName(enc, "target_app_instance_id"); err != nil {
			return err
		}
		if err := writeString(enc, c.TargetAppInstanceID); err != nil {
			return err
		}
		if err := writeName(enc, "target_display_id"); err != nil {
			return err
		}
		if c.TargetDisplayID == nil {
			if err := writeNull(enc); err != nil {
				return err
			}
		} else if err := writeInt(enc, *c.TargetDisplayID); err != nil {
			return err
		}
		if err := writeName(enc, "target_generation"); err != nil {
			return err
		}
		if err := writeString(enc, c.TargetGeneration); err != nil {
			return err
		}
	case "cancel_create":
		if c.TargetAppInstanceID == "" || c.TargetCreateCommandID == "" {
			return fmt.Errorf("cancel_create requires target_app_instance_id and target_create_command_id")
		}
		if err := writeName(enc, "target_app_instance_id"); err != nil {
			return err
		}
		if err := writeString(enc, c.TargetAppInstanceID); err != nil {
			return err
		}
		if err := writeName(enc, "target_create_command_id"); err != nil {
			return err
		}
		if err := writeString(enc, c.TargetCreateCommandID); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Availability is the control-view readiness projection.
type Availability struct {
	State  string `json:"state"` // ready / recovering / blocked
	Reason string `json:"reason,omitempty"`
}

// ControlSession is the short control-view session projection.
type ControlSession struct {
	Origin    string `json:"origin"`
	Agent     string `json:"agent,omitempty"`
	Mode      string `json:"mode"`
	State     string `json:"state"`
	StartedAt string `json:"started_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ControlWork is the current ordinary device work projection. AppKind names the
// app-tool branch when Kind is "app"; it never carries device facts.
type ControlWork struct {
	OperationID string `json:"operation_id"`
	Kind        string `json:"kind"`
	AppKind     string `json:"app_kind,omitempty"`
	Phase       string `json:"phase"`
}

// ControlCleanup is the minimal cleanup summary.
type ControlCleanup struct {
	State     string `json:"state"`
	Execution string `json:"execution,omitempty"`
	Tasks     string `json:"tasks,omitempty"`
	Recents   string `json:"recents,omitempty"`
	Display   string `json:"display,omitempty"`
}

// ControlView is the single immutable control projection consumed by the App.
type ControlView struct {
	Revision     int64           `json:"revision"`
	Availability Availability    `json:"availability"`
	Session      *ControlSession `json:"session"`
	Work         *ControlWork    `json:"work"`
	Cleanup      *ControlCleanup `json:"cleanup"`
}

// AppStopAck is the minimal stop confirmation for the App.
type AppStopAck struct {
	Seq   int64  `json:"seq"`
	State string `json:"state"` // accepted / duplicate
}

// AppSyncResponse is the control heartbeat response.
type AppSyncResponse struct {
	ContractVersion   string       `json:"contract_version"`
	ServiceInstanceID string       `json:"service_instance_id"`
	Control           ControlView  `json:"control"`
	Commands          []AppCommand `json:"commands"`
	Messages          []AppMessage `json:"messages"`
	Stop              *AppStopAck  `json:"stop,omitempty"`
}

// ControlStopRequest is the stable minimal stop carrier.
type ControlStopRequest struct {
	ContractVersion string `json:"contract_version"`
	AppInstanceID   string `json:"app_instance_id"`
	StopSeq         int64  `json:"stop_seq"`
	Reason          string `json:"reason"`
}

// ControlStopResponse confirms the service bound the stop boundary. It does
// not claim physical resource release and carries no ordinary device state.
type ControlStopResponse struct {
	ContractVersion   string `json:"contract_version"`
	ServiceInstanceID string `json:"service_instance_id"`
	Accepted          bool   `json:"accepted"`
	StopSeq           int64  `json:"stop_seq"`
	State             string `json:"state"` // revoked / recorded / duplicate
}
