package protocol

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"unicode/utf8"
)

// The request types in this package are the single public model shared by the
// HTTP client and the MCP adapter. Their encoders write exactly the members
// that tool admits, so a caller cannot accidentally send a private field, an
// explicit null or an alias the service would reject. Decoding stays with the
// strict Decode* functions; these encoders only canonicalize an already
// admitted request before it is sent.

// encodeRequest runs one object encoder and returns its bytes.
func encodeRequest(write func(enc *jsontext.Encoder) error) ([]byte, error) {
	var buffer bytes.Buffer
	enc := jsontext.NewEncoder(&buffer)
	if err := write(enc); err != nil {
		return nil, err
	}
	// The encoder terminates a top-level value with a newline; the request
	// body is the value alone.
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// Encode writes the canonical start() body.
func (r *StartRequest) Encode() ([]byte, error) {
	return []byte("{}"), nil
}

// Encode writes the canonical status(reference=None) body.
func (r *StatusRequest) Encode() ([]byte, error) {
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if r.HasRef {
			if err := writeName(enc, "reference"); err != nil {
				return err
			}
			if err := writeString(enc, r.Reference); err != nil {
				return err
			}
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// Encode writes the canonical observe(reference, full, image, wait_ms) body.
func (r *ObserveRequest) Encode() ([]byte, error) {
	if r.Reference == "" {
		return nil, errors.New("observe requires a reference")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "reference"); err != nil {
			return err
		}
		if err := writeString(enc, r.Reference); err != nil {
			return err
		}
		if err := writeName(enc, "full"); err != nil {
			return err
		}
		if err := writeBool(enc, r.Full); err != nil {
			return err
		}
		if err := writeName(enc, "image"); err != nil {
			return err
		}
		if err := writeBool(enc, r.Image); err != nil {
			return err
		}
		if err := writeName(enc, "wait_ms"); err != nil {
			return err
		}
		if err := writeInt(enc, r.WaitMS); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// Encode writes the canonical wait_for(reference, query, absent_query, timeout,
// stable, full, image) body. Omitted conditions are simply absent members.
func (r *WaitForRequest) Encode() ([]byte, error) {
	if r.Reference == "" {
		return nil, errors.New("wait_for requires a reference")
	}
	if r.Timeout < MinWaitTimeoutSeconds || r.Timeout > MaxWaitTimeoutSeconds {
		return nil, errors.New("wait_for timeout is out of range")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "reference"); err != nil {
			return err
		}
		if err := writeString(enc, r.Reference); err != nil {
			return err
		}
		if r.HasQuery {
			if err := writeName(enc, "query"); err != nil {
				return err
			}
			if err := writeString(enc, r.Query); err != nil {
				return err
			}
		}
		if r.HasAbsentQuery {
			if err := writeName(enc, "absent_query"); err != nil {
				return err
			}
			if err := writeString(enc, r.AbsentQuery); err != nil {
				return err
			}
		}
		if err := writeName(enc, "timeout"); err != nil {
			return err
		}
		if err := writeInt(enc, r.Timeout); err != nil {
			return err
		}
		if err := writeName(enc, "stable"); err != nil {
			return err
		}
		if err := writeBool(enc, r.Stable); err != nil {
			return err
		}
		if err := writeName(enc, "full"); err != nil {
			return err
		}
		if err := writeBool(enc, r.Full); err != nil {
			return err
		}
		if err := writeName(enc, "image"); err != nil {
			return err
		}
		if err := writeBool(enc, r.Image); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// Encode writes the canonical snap(reference) body.
func (r *SnapRequest) Encode() ([]byte, error) {
	if r.Reference == "" {
		return nil, errors.New("snap requires a reference")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "reference"); err != nil {
			return err
		}
		if err := writeString(enc, r.Reference); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// Encode writes the canonical end(reference) body.
func (r *EndRequest) Encode() ([]byte, error) {
	if r.Reference == "" {
		return nil, errors.New("end requires a reference")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "reference"); err != nil {
			return err
		}
		if err := writeString(enc, r.Reference); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// Encode writes the canonical act(reference, operation, image) body.
func (r *ActRequest) Encode() ([]byte, error) {
	if r.Reference == "" {
		return nil, errors.New("act requires a reference")
	}
	if err := r.Operation.ValidateEffectiveDefaults(); err != nil {
		return nil, errors.New("act operation has an invalid branch member")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "reference"); err != nil {
			return err
		}
		if err := writeString(enc, r.Reference); err != nil {
			return err
		}
		if err := writeName(enc, "operation"); err != nil {
			return err
		}
		if err := encodeOperation(enc, &r.Operation); err != nil {
			return err
		}
		if err := writeName(enc, "image"); err != nil {
			return err
		}
		if err := writeBool(enc, r.Image); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// encodeOperation writes one strict action branch.
func encodeOperation(enc *jsontext.Encoder, op *Operation) error {
	if op == nil {
		return errors.New("act requires an operation")
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "type"); err != nil {
		return err
	}
	if err := writeString(enc, op.Type); err != nil {
		return err
	}
	switch op.Type {
	case "tap", "long_press":
		if err := rejectScrollMembers(op); err != nil {
			return err
		}
		if err := rejectSwipeDuration(op); err != nil {
			return err
		}
		if op.HasPressEnter {
			return errors.New("press_enter is only valid for text")
		}
		if op.HasBubble && (op.Target == nil || !op.Target.UseRef) {
			return errors.New("bubble is only valid for a ref target")
		}
		if err := writeTargetOrPoint(enc, "target", op.Target, nil); err != nil {
			return err
		}
		if op.HasBubble {
			if err := writeName(enc, "bubble"); err != nil {
				return err
			}
			if err := writeBool(enc, op.Bubble); err != nil {
				return err
			}
		}
	case "text":
		if err := rejectScrollMembers(op); err != nil {
			return err
		}
		if err := rejectSwipeDuration(op); err != nil {
			return err
		}
		if op.HasBubble {
			return errors.New("bubble is only valid for tap or long_press")
		}
		if err := writeTargetOrPoint(enc, "target", op.Target, nil); err != nil {
			return err
		}
		if err := writeName(enc, "text"); err != nil {
			return err
		}
		if err := writeString(enc, op.Text); err != nil {
			return err
		}
		if op.HasPressEnter {
			if err := writeName(enc, "press_enter"); err != nil {
				return err
			}
			if err := writeBool(enc, op.PressEnter); err != nil {
				return err
			}
		}
	case "swipe":
		if op.Target != nil || op.Container != nil || op.Direction != "" || op.Amount != "" {
			return errors.New("swipe accepts only from, to and duration_ms")
		}
		if op.From == nil || op.To == nil {
			return errors.New("swipe requires from and to")
		}
		if *op.From == *op.To {
			return errors.New("swipe from and to must differ")
		}
		if op.DurationMS < 0 ||
			(op.DurationMS > 0 && (op.DurationMS < MinSwipeDurationMS || op.DurationMS > MaxSwipeDurationMS)) {
			return errors.New("swipe duration_ms is out of range")
		}
		if err := writePointField(enc, "from", op.From); err != nil {
			return err
		}
		if err := writePointField(enc, "to", op.To); err != nil {
			return err
		}
		if op.DurationMS > 0 {
			if err := writeName(enc, "duration_ms"); err != nil {
				return err
			}
			if err := writeInt(enc, op.DurationMS); err != nil {
				return err
			}
		}
	case "scroll":
		if op.Target != nil || op.From != nil || op.To != nil || op.Key != "" || op.Text != "" {
			return errors.New("scroll accepts only container, direction and amount")
		}
		if err := rejectSwipeDuration(op); err != nil {
			return err
		}
		if op.Container == nil {
			return errors.New("scroll requires a container")
		}
		if err := validateContainer(op.Container); err != nil {
			return err
		}
		if !validScrollDirection(op.Direction) {
			return errors.New("scroll requires a valid direction")
		}
		if op.Amount != "" && op.Amount != ScrollAmountPage && op.Amount != ScrollAmountHalf {
			return errors.New("scroll amount must be page or half")
		}
		if err := writeTargetOrPoint(enc, "container", op.Container, nil); err != nil {
			return err
		}
		if err := writeName(enc, "direction"); err != nil {
			return err
		}
		if err := writeString(enc, op.Direction); err != nil {
			return err
		}
		if op.Amount != "" {
			if err := writeName(enc, "amount"); err != nil {
				return err
			}
			if err := writeString(enc, op.Amount); err != nil {
				return err
			}
		}
	case "key":
		if err := rejectScrollMembers(op); err != nil {
			return err
		}
		if err := rejectSwipeDuration(op); err != nil {
			return err
		}
		if err := writeName(enc, "key"); err != nil {
			return err
		}
		if err := writeString(enc, op.Key); err != nil {
			return err
		}
	default:
		return errors.New("unsupported operation type")
	}
	return enc.WriteToken(jsontext.EndObject)
}

// rejectScrollMembers refuses the scroll-only members on another branch.
func rejectScrollMembers(op *Operation) error {
	if op.Container != nil {
		return errors.New("container is only valid for scroll")
	}
	if op.Direction != "" {
		return errors.New("direction is only valid for scroll")
	}
	if op.Amount != "" {
		return errors.New("amount is only valid for scroll")
	}
	return nil
}

// rejectSwipeDuration refuses duration_ms on a non-swipe branch.
func rejectSwipeDuration(op *Operation) error {
	if op.DurationMS != 0 {
		return errors.New("duration_ms is only valid for swipe")
	}
	return nil
}

// validateContainer checks the caller-supplied scroll container selector.
func validateContainer(container *Target) error {
	if container.UseRef {
		if container.Ref == "" {
			return errors.New("scroll container ref must not be empty")
		}
		return nil
	}
	if container.X < 0 || container.Y < 0 {
		return errors.New("scroll container coordinates must not be negative")
	}
	return nil
}

// validScrollDirection reports whether direction is one of the four values.
func validScrollDirection(direction string) bool {
	switch direction {
	case ScrollDirectionUp, ScrollDirectionDown, ScrollDirectionLeft, ScrollDirectionRight:
		return true
	}
	return false
}

// writeTargetOrPoint writes the strict reference-or-coordinate target.
func writeTargetOrPoint(enc *jsontext.Encoder, name string, target *Target, point *Point) error {
	if target == nil && point == nil {
		return errors.New("operation target is missing")
	}
	if err := writeName(enc, name); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if target != nil && target.UseRef {
		if err := writeName(enc, "ref"); err != nil {
			return err
		}
		if err := writeString(enc, target.Ref); err != nil {
			return err
		}
	} else {
		var x, y int
		if target != nil {
			x, y = target.X, target.Y
		} else {
			x, y = point.X, point.Y
		}
		if err := writeName(enc, "x"); err != nil {
			return err
		}
		if err := writeInt(enc, x); err != nil {
			return err
		}
		if err := writeName(enc, "y"); err != nil {
			return err
		}
		if err := writeInt(enc, y); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// writePointField writes one coordinate object.
func writePointField(enc *jsontext.Encoder, name string, point *Point) error {
	if err := writeName(enc, name); err != nil {
		return err
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "x"); err != nil {
		return err
	}
	if err := writeInt(enc, point.X); err != nil {
		return err
	}
	if err := writeName(enc, "y"); err != nil {
		return err
	}
	if err := writeInt(enc, point.Y); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Encode writes the canonical inspect(reference, node, query) body.
func (r *InspectRequest) Encode() ([]byte, error) {
	if r.Reference == "" {
		return nil, errors.New("inspect requires a reference")
	}
	if r.HasNode && r.HasQuery {
		return nil, errors.New("inspect node and query are mutually exclusive")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "reference"); err != nil {
			return err
		}
		if err := writeString(enc, r.Reference); err != nil {
			return err
		}
		if r.HasNode {
			if err := writeName(enc, "node"); err != nil {
				return err
			}
			if err := writeString(enc, r.Node); err != nil {
				return err
			}
		}
		if r.HasQuery {
			if err := writeName(enc, "query"); err != nil {
				return err
			}
			if err := writeString(enc, r.Query); err != nil {
				return err
			}
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// Encode writes the canonical keepalive(reference, duration_minutes) body.
func (r *KeepaliveRequest) Encode() ([]byte, error) {
	if r.Reference == "" {
		return nil, errors.New("keepalive requires a reference")
	}
	if _, apiErr := r.EffectiveDurationMinutes(); apiErr != nil {
		return nil, apiErr
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "reference"); err != nil {
			return err
		}
		if err := writeString(enc, r.Reference); err != nil {
			return err
		}
		if r.HasDurationMinutes {
			if err := writeName(enc, "duration_minutes"); err != nil {
				return err
			}
			if err := writeInt(enc, r.DurationMinutes); err != nil {
				return err
			}
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// Encode writes the canonical diagnose(reference=None) body.
func (r *DiagnoseRequest) Encode() ([]byte, error) {
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if r.HasRef {
			if err := writeName(enc, "reference"); err != nil {
				return err
			}
			if err := writeString(enc, r.Reference); err != nil {
				return err
			}
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// validateAppRequest applies the same closed kind matrix the service decoder
// enforces to a typed request before encoding. Without it a caller that fills a
// member outside the selected branch would send a different request than the
// struct describes: the encoder writes only branch members, so the extra value
// would disappear instead of being rejected.
func validateAppRequest(r *AppRequest) error {
	if r == nil {
		return errors.New("app request is required")
	}
	allowed := appKindMembers(r.Kind)
	if allowed == nil {
		return errors.New("unsupported app kind")
	}
	if !allowed["query"] && (r.HasQuery || r.Query != "") {
		return errors.New(r.Kind + " does not accept query")
	}
	if allowed["query"] {
		if r.Query != "" && !r.HasQuery {
			return errors.New("app.query is set without HasQuery")
		}
		if utf8.RuneCountInString(r.Query) > MaxQueryRunes {
			return errors.New("app.query exceeds the length limit")
		}
	}
	if !allowed["reference"] && r.Reference != "" {
		return errors.New(r.Kind + " does not accept reference")
	}
	if allowed["reference"] {
		if apiErr := checkReference(r.Reference); apiErr != nil {
			return errors.New(apiErr.Message)
		}
	}
	if !allowed["scope"] && r.Scope != "" {
		return errors.New(r.Kind + " does not accept scope")
	}
	if allowed["scope"] && r.Scope != "" && r.Scope != AppScopeSession && r.Scope != AppScopePrimary {
		return errors.New("app.scope must be session or primary")
	}
	if !allowed["app"] && (r.HasFilter || r.FilterApp != "") {
		return errors.New(r.Kind + " does not accept app")
	}
	if allowed["app"] {
		if !r.HasFilter && r.FilterApp != "" {
			return errors.New("app filter is set without HasFilter")
		}
		if r.HasFilter && !ValidPackageName(r.FilterApp) {
			return errors.New("app filter must be an explicit package name")
		}
	}
	if !allowed["target"] && r.Target != nil {
		return errors.New(r.Kind + " does not accept target")
	}
	if allowed["target"] {
		if r.Target == nil {
			return errors.New(r.Kind + " requires target")
		}
		if r.Target.UseTask {
			if r.Target.TaskID <= 0 || r.Target.App != "" {
				return errors.New("app task target requires one positive task_id")
			}
			if r.Kind == AppKindOpen {
				return errors.New("app.open only accepts a package target")
			}
		} else {
			if r.Target.TaskID != 0 || !ValidPackageName(r.Target.App) {
				return errors.New("app package target requires one explicit package")
			}
			if r.Kind == AppKindKill {
				return errors.New("app.kill requires a task id target")
			}
		}
	}
	if !allowed["restore_foreground"] && r.RestoreForeground {
		return errors.New(r.Kind + " does not accept restore_foreground")
	}
	if !allowed["allow_borrowed"] && r.AllowBorrowed {
		return errors.New(r.Kind + " does not accept allow_borrowed")
	}
	if !allowed["allow_stop_playback"] && r.AllowStopPlayback {
		return errors.New(r.Kind + " does not accept allow_stop_playback")
	}
	return nil
}

// Encode writes the canonical app kind branch body.
func (r *AppRequest) Encode() ([]byte, error) {
	if err := validateAppRequest(r); err != nil {
		return nil, err
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "kind"); err != nil {
			return err
		}
		if err := writeString(enc, r.Kind); err != nil {
			return err
		}
		switch r.Kind {
		case AppKindFind:
			if r.HasQuery {
				if err := writeName(enc, "query"); err != nil {
					return err
				}
				if err := writeString(enc, r.Query); err != nil {
					return err
				}
			}
		case AppKindList:
			if r.Reference == "" {
				return errors.New("app.list requires reference")
			}
			if err := writeName(enc, "reference"); err != nil {
				return err
			}
			if err := writeString(enc, r.Reference); err != nil {
				return err
			}
			if r.Scope != "" {
				if err := writeName(enc, "scope"); err != nil {
					return err
				}
				if err := writeString(enc, r.Scope); err != nil {
					return err
				}
			}
			if r.HasFilter {
				if err := writeName(enc, "app"); err != nil {
					return err
				}
				if err := writeString(enc, r.FilterApp); err != nil {
					return err
				}
			}
		case AppKindOpen:
			if r.Reference == "" || r.Target == nil || !r.Target.UseTask && r.Target.App == "" {
				return errors.New("app.open requires reference and target.app")
			}
			if r.Target.UseTask {
				return errors.New("app.open only accepts a package target")
			}
			if err := writeName(enc, "reference"); err != nil {
				return err
			}
			if err := writeString(enc, r.Reference); err != nil {
				return err
			}
			if err := writeName(enc, "target"); err != nil {
				return err
			}
			if err := encodeAppTarget(enc, r.Kind, r.Target); err != nil {
				return err
			}
		case AppKindClaim, AppKindHandoff, AppKindClose, AppKindKill:
			if r.Reference == "" || r.Target == nil {
				return errors.New("app target kind requires reference and target")
			}
			if err := writeName(enc, "reference"); err != nil {
				return err
			}
			if err := writeString(enc, r.Reference); err != nil {
				return err
			}
			if err := writeName(enc, "target"); err != nil {
				return err
			}
			if err := encodeAppTarget(enc, r.Kind, r.Target); err != nil {
				return err
			}
			if err := encodeAppOptions(enc, r); err != nil {
				return err
			}
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// encodeAppTarget writes the single admitted selector object.
func encodeAppTarget(enc *jsontext.Encoder, kind string, target *AppTarget) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if target.UseTask {
		if target.TaskID <= 0 {
			return errors.New("app target task id must be positive")
		}
		if err := writeName(enc, "task_id"); err != nil {
			return err
		}
		if err := writeInt(enc, target.TaskID); err != nil {
			return err
		}
	} else {
		if kind == AppKindKill {
			return errors.New("app.kill requires a task id target")
		}
		if target.App == "" {
			return errors.New("app target requires a package")
		}
		if err := writeName(enc, "app"); err != nil {
			return err
		}
		if err := writeString(enc, target.App); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// encodeAppOptions writes the explicit boolean options of one branch.
func encodeAppOptions(enc *jsontext.Encoder, r *AppRequest) error {
	option := func(name string, value bool) error {
		if !value {
			return nil
		}
		if err := writeName(enc, name); err != nil {
			return err
		}
		return writeBool(enc, true)
	}
	if r.Kind == AppKindHandoff && r.RestoreForeground {
		return option("restore_foreground", true)
	}
	if r.Kind == AppKindClose {
		return option("allow_borrowed", r.AllowBorrowed)
	}
	if r.Kind == AppKindKill {
		return option("allow_stop_playback", r.AllowStopPlayback)
	}
	return nil
}

// Encode writes the canonical notify(text) body.
func (r *NotifyRequest) Encode() ([]byte, error) {
	if r.Text == "" {
		return nil, errors.New("notify requires text")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "text"); err != nil {
			return err
		}
		if err := writeString(enc, r.Text); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}

// Encode writes the canonical shell(reference, command, root) body.
func (r *ShellRequest) Encode() ([]byte, error) {
	if r.Reference == "" || r.Command == "" {
		return nil, errors.New("shell requires reference and command")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "reference"); err != nil {
			return err
		}
		if err := writeString(enc, r.Reference); err != nil {
			return err
		}
		if err := writeName(enc, "command"); err != nil {
			return err
		}
		if err := writeString(enc, r.Command); err != nil {
			return err
		}
		if err := writeName(enc, "root"); err != nil {
			return err
		}
		if err := writeBool(enc, r.Root); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	})
}
