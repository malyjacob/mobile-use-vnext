package protocol

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"
	"unicode/utf8"
)

// The maximum sizes are fixed internal policy; callers set no timeout/TTL.
const (
	MaxTextRunes    = 4096
	MaxQueryRunes   = 256
	MaxCommandRunes = 4096
	MaxWaitMS       = 10000
	MaxReferenceLen = 64
	// MaxSubmitRefLen bounds the text-only submit_ref member on its own; the
	// value shares no budget with a session reference or any other member.
	MaxSubmitRefLen = 64

	// Wait timeout bounds in seconds. The upper bound keeps one synchronous
	// wait inside the fixed 90s HTTP ceiling once the bounded verification
	// acquisition of phase C is added; the lower bound is the shortest useful
	// wait and is raised to 2s when a three-frame stability gate is required.
	MinWaitTimeoutSeconds = 1
	MaxWaitTimeoutSeconds = 40
	// MinStableWaitTimeoutSeconds is the shortest timeout that can contain the
	// fixed three-frame stability gate.
	MinStableWaitTimeoutSeconds = 2
)

// StartRequest is start(); the object must be empty.
type StartRequest struct{}

// StatusRequest is status(reference=None).
type StatusRequest struct {
	Reference string
	HasRef    bool
}

// ObserveRequest is observe(reference, full=false, image=false, wait_ms=0).
type ObserveRequest struct {
	Reference string
	Full      bool
	Image     bool
	WaitMS    int
}

// WaitForRequest is wait_for(reference, query, absent_query, timeout, stable,
// full, image). query and absent_query are the optional literal conditions, and
// the two Has flags record whether the caller supplied them at all so an
// omitted condition is distinguishable from an empty one.
type WaitForRequest struct {
	Reference      string
	Query          string
	HasQuery       bool
	AbsentQuery    string
	HasAbsentQuery bool
	Timeout        int
	Stable         bool
	Full           bool
	Image          bool
}

// SnapRequest is snap(reference): one read-only image read of the display bound
// to the referenced original session.
type SnapRequest struct {
	Reference string
}

// EndRequest is end(reference).
type EndRequest struct {
	Reference string
}

// Scroll direction values name the requested reading/viewport direction. They
// are case-sensitive; the service never proves a device scroll axis from them.
const (
	ScrollDirectionUp    = "up"
	ScrollDirectionDown  = "down"
	ScrollDirectionLeft  = "left"
	ScrollDirectionRight = "right"
)

// Scroll amount values bound one controlled scroll attempt.
const (
	ScrollAmountPage = "page"
	ScrollAmountHalf = "half"
)

// Swipe duration bounds in milliseconds. An omitted duration means
// DefaultSwipeDurationMS; zero is not a valid explicit duration.
const (
	DefaultSwipeDurationMS = 300
	MinSwipeDurationMS     = 50
	MaxSwipeDurationMS     = 2000
)

// SubmitRefEnterKey is the closed submit_ref literal that names the one bounded
// ENTER submission path. It is a fixed lowercase token and can never collide
// with the public node-ref grammar (ValidPublicRef).
const SubmitRefEnterKey = "enter_key"

// Target is exactly one of a public ref or absolute screen coordinates.
type Target struct {
	UseRef bool
	Ref    string
	X, Y   int
}

// Operation is one strict act branch.
type Operation struct {
	Type   string
	Target *Target
	From   *Point
	To     *Point
	Key    string
	Text   string
	// Container is the scroll-only container selector; it has the same
	// reference-or-coordinate semantics as Target.
	Container *Target
	// Direction is the scroll-only reading direction; it is one of the
	// ScrollDirection* values.
	Direction string
	// Amount is the scroll-only travel amount: ScrollAmountPage (the decode
	// default) or ScrollAmountHalf.
	Amount string
	// DurationMS is the swipe-only gesture duration. An omitted wire duration
	// decodes to DefaultSwipeDurationMS; the encoder omits it when zero.
	DurationMS int
	// Bubble is the presence-aware tap/long_press member. It only applies to a
	// ref target: omitted means effective true, explicit false disables the one
	// nearest-interactive-ancestor lift, and an explicit member on a coordinate
	// target or any other operation type is rejected.
	Bubble    bool
	HasBubble bool
	// AutoSubmit is the presence-aware text member. Omitted means effective
	// true (one bounded submission after an exact readback); explicit false
	// stops after the input path and semantically ignores SubmitRef. Other
	// operation types cannot carry it.
	AutoSubmit    bool
	HasAutoSubmit bool
	// SubmitRef is the presence-aware text member that names the submission
	// target: SubmitRefEnterKey (the default) or one public n-prefixed node
	// ref. Other operation types cannot carry it. The value is always
	// validated; it is ignored only when AutoSubmit is effectively false.
	SubmitRef    string
	HasSubmitRef bool
}

// EffectiveBubble returns the branch-qualified value. It is meaningful only
// for a ref tap/long_press; callers use ValidateEffectiveDefaults first.
func (o *Operation) EffectiveBubble() bool {
	return o == nil || !o.HasBubble || o.Bubble
}

// EffectiveAutoSubmit returns the branch-qualified submission switch. It is
// meaningful only for text; callers use ValidateEffectiveDefaults first.
func (o *Operation) EffectiveAutoSubmit() bool {
	return o == nil || !o.HasAutoSubmit || o.AutoSubmit
}

// EffectiveSubmitRef returns the branch-qualified submission selector. An
// omitted member defaults to SubmitRefEnterKey. It is meaningful only for
// text; the value is validated for every text operation, but callers gate the
// submission path with EffectiveAutoSubmit because an effectively false switch
// ignores the selector entirely.
func (o *Operation) EffectiveSubmitRef() string {
	if o == nil || !o.HasSubmitRef || o.SubmitRef == "" {
		return SubmitRefEnterKey
	}
	return o.SubmitRef
}

// IsSubmitTap reports whether this operation submits by tapping a public node:
// it is a text operation, AutoSubmit is effectively true, and the effective
// submit_ref is not SubmitRefEnterKey. It never looks a node up and never
// proves the ref exists; callers use ValidateEffectiveDefaults first so a
// present value is one of the two admitted shapes.
func (o *Operation) IsSubmitTap() bool {
	if o == nil || o.Type != "text" || !o.EffectiveAutoSubmit() {
		return false
	}
	return o.EffectiveSubmitRef() != SubmitRefEnterKey
}

// ValidateEffectiveDefaults checks the closed membership rules shared by HTTP
// decode, the canonical encoder, MCP argument conversion and direct engine
// calls. It never computes a default for a branch where the member is invalid.
func (o *Operation) ValidateEffectiveDefaults() *APIError {
	if o == nil {
		return Invalid("operation 必须是对象")
	}
	switch o.Type {
	case "tap", "long_press":
		if o.HasAutoSubmit || o.HasSubmitRef {
			return Invalid("auto_submit 与 submit_ref 只适用于 text")
		}
		if o.HasBubble && (o.Target == nil || !o.Target.UseRef) {
			return Invalid("bubble 只适用于 ref 目标；坐标目标不接受 bubble")
		}
	case "text":
		if o.HasBubble {
			return Invalid("bubble 只适用于 tap 或 long_press 的 ref 目标")
		}
		// The syntax is checked for every text operation, including
		// auto_submit=false, where the member is then ignored.
		if o.HasSubmitRef {
			if apiErr := checkSubmitRef(o.SubmitRef); apiErr != nil {
				return apiErr
			}
		}
	default:
		if o.HasBubble {
			return Invalid("bubble 只适用于 tap 或 long_press 的 ref 目标")
		}
		if o.HasAutoSubmit || o.HasSubmitRef {
			return Invalid("auto_submit 与 submit_ref 只适用于 text")
		}
	}
	return nil
}

// checkSubmitRef admits exactly the closed ENTER literal or one public node
// ref, never an empty or over-long value.
func checkSubmitRef(ref string) *APIError {
	if ref == "" {
		return Invalid("submit_ref 不能为空")
	}
	if len(ref) > MaxSubmitRefLen {
		return Invalid("submit_ref 长度超限").WithDetail("limit", MaxSubmitRefLen)
	}
	if ref == SubmitRefEnterKey {
		return nil
	}
	if !ValidPublicRef(ref) {
		return Invalid("submit_ref 只接受 enter_key 或公开节点编号")
	}
	return nil
}

// Point is one absolute screen coordinate.
type Point struct{ X, Y int }

// ActRequest is act(reference, operation, image=false).
type ActRequest struct {
	Reference string
	Operation Operation
	Image     bool
}

// Keys is the fixed first-version key whitelist.
var Keys = map[string]bool{
	"BACK": true, "ENTER": true, "DEL": true, "TAB": true, "ESCAPE": true,
	"DPAD_UP": true, "DPAD_DOWN": true, "HOME": true, "APP_SWITCH": true,
}

// DecodeStart admits an empty JSON object only.
func DecodeStart(body []byte) (*StartRequest, *APIError) {
	var presence struct{}
	if err := decodeStrict(body, &presence, &presence); err != nil {
		return nil, invalidDecode(err)
	}
	return &StartRequest{}, nil
}

// DecodeStatus admits status(reference).
func DecodeStatus(body []byte) (*StatusRequest, *APIError) {
	var presence struct {
		Reference jsontext.Value `json:"reference"`
	}
	var wire struct {
		Reference string `json:"reference"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Reference) > 0 && string(presence.Reference) == "null" {
		return nil, Invalid("reference 不接受 null；省略该字段或提供编号")
	}
	req := &StatusRequest{Reference: wire.Reference, HasRef: len(presence.Reference) > 0}
	if req.HasRef {
		if err := checkReference(req.Reference); err != nil {
			return nil, err
		}
	}
	return req, nil
}

// DecodeObserve admits observe(reference, full, image, wait_ms).
func DecodeObserve(body []byte) (*ObserveRequest, *APIError) {
	var presence struct {
		Reference jsontext.Value `json:"reference"`
		Full      jsontext.Value `json:"full"`
		Image     jsontext.Value `json:"image"`
		WaitMS    jsontext.Value `json:"wait_ms"`
	}
	var wire struct {
		Reference string `json:"reference"`
		Full      bool   `json:"full"`
		Image     bool   `json:"image"`
		WaitMS    int    `json:"wait_ms"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Reference) == 0 {
		return nil, Invalid("observe 需要 reference 参数")
	}
	if string(presence.Reference) == "null" {
		return nil, Invalid("reference 不接受 null")
	}
	if err := checkNulls(map[string]jsontext.Value{
		"full": presence.Full, "image": presence.Image, "wait_ms": presence.WaitMS,
	}); err != nil {
		return nil, err
	}
	if err := checkReference(wire.Reference); err != nil {
		return nil, err
	}
	if wire.WaitMS < 0 || wire.WaitMS > MaxWaitMS {
		return nil, Invalid("wait_ms 超出允许范围").WithDetail("limit_ms", MaxWaitMS)
	}
	return &ObserveRequest{Reference: wire.Reference, Full: wire.Full,
		Image: wire.Image, WaitMS: wire.WaitMS}, nil
}

// DecodeWaitFor admits wait_for(reference, query, absent_query, timeout, stable,
// full, image). timeout is the only required member besides reference; every
// other condition is optional and an omitted condition is not the same as an
// empty one.
func DecodeWaitFor(body []byte) (*WaitForRequest, *APIError) {
	var presence struct {
		Reference   jsontext.Value `json:"reference"`
		Query       jsontext.Value `json:"query"`
		AbsentQuery jsontext.Value `json:"absent_query"`
		Timeout     jsontext.Value `json:"timeout"`
		Stable      jsontext.Value `json:"stable"`
		Full        jsontext.Value `json:"full"`
		Image       jsontext.Value `json:"image"`
	}
	var wire struct {
		Reference   string `json:"reference"`
		Query       string `json:"query"`
		AbsentQuery string `json:"absent_query"`
		Timeout     int    `json:"timeout"`
		Stable      bool   `json:"stable"`
		Full        bool   `json:"full"`
		Image       bool   `json:"image"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Reference) == 0 {
		return nil, Invalid("wait_for 需要 reference 参数")
	}
	if string(presence.Reference) == "null" {
		return nil, Invalid("reference 不接受 null")
	}
	if len(presence.Timeout) == 0 {
		return nil, Invalid("wait_for 需要 timeout 参数")
	}
	if string(presence.Timeout) == "null" {
		return nil, Invalid("timeout 不接受 null")
	}
	if err := checkNulls(map[string]jsontext.Value{
		"query": presence.Query, "absent_query": presence.AbsentQuery,
		"stable": presence.Stable, "full": presence.Full, "image": presence.Image,
	}); err != nil {
		return nil, err
	}
	if err := checkReference(wire.Reference); err != nil {
		return nil, err
	}
	if wire.Timeout < MinWaitTimeoutSeconds || wire.Timeout > MaxWaitTimeoutSeconds {
		return nil, Invalid("timeout 超出允许范围").WithDetail("limit_s", MaxWaitTimeoutSeconds)
	}
	req := &WaitForRequest{
		Reference: wire.Reference, Timeout: wire.Timeout,
		Stable: wire.Stable, Full: wire.Full, Image: wire.Image,
	}
	if len(presence.Query) > 0 {
		if apiErr := checkWaitQuery("query", wire.Query); apiErr != nil {
			return nil, apiErr
		}
		req.Query, req.HasQuery = wire.Query, true
	}
	if len(presence.AbsentQuery) > 0 {
		if apiErr := checkWaitQuery("absent_query", wire.AbsentQuery); apiErr != nil {
			return nil, apiErr
		}
		req.AbsentQuery, req.HasAbsentQuery = wire.AbsentQuery, true
	}
	if req.Stable && req.Timeout < MinStableWaitTimeoutSeconds {
		return nil, Invalid("stable=true 时 timeout 不能小于 2 秒").
			WithDetail("limit_s", MinStableWaitTimeoutSeconds)
	}
	if req.HasQuery && req.HasAbsentQuery && req.Query == req.AbsentQuery {
		return nil, Invalid("query 与 absent_query 不能相同")
	}
	return req, nil
}

// checkWaitQuery admits one literal condition. It is deliberately stricter than
// the read-only inspection query: an all-whitespace condition can never be a
// meaningful wait and is rejected before any device work.
func checkWaitQuery(name, value string) *APIError {
	if utf8.RuneCountInString(value) > MaxQueryRunes {
		return Invalid(name+" 超过长度上限").WithDetail("limit", MaxQueryRunes)
	}
	if strings.TrimSpace(value) == "" {
		return Invalid(name + " 不能为空或全空白")
	}
	return nil
}

// DecodeSnap admits snap(reference). The reference is required and keeps the
// ordinary O/P reference shape; it resolves the original session, while the
// returned image always reads the current screen.
func DecodeSnap(body []byte) (*SnapRequest, *APIError) {
	var presence struct {
		Reference jsontext.Value `json:"reference"`
	}
	var wire struct {
		Reference string `json:"reference"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Reference) == 0 {
		return nil, Invalid("snap 需要 reference 参数")
	}
	if string(presence.Reference) == "null" {
		return nil, Invalid("reference 不接受 null")
	}
	if err := checkReference(wire.Reference); err != nil {
		return nil, err
	}
	return &SnapRequest{Reference: wire.Reference}, nil
}

// DecodeEnd admits end(reference).
func DecodeEnd(body []byte) (*EndRequest, *APIError) {
	var presence struct {
		Reference jsontext.Value `json:"reference"`
	}
	var wire struct {
		Reference string `json:"reference"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Reference) == 0 {
		return nil, Invalid("end 需要 reference 参数")
	}
	if string(presence.Reference) == "null" {
		return nil, Invalid("reference 不接受 null")
	}
	if err := checkReference(wire.Reference); err != nil {
		return nil, err
	}
	return &EndRequest{Reference: wire.Reference}, nil
}

// DecodeAct admits act(reference, operation, image). The retired
// observation_id member is not an alias: it is an unknown field.
func DecodeAct(body []byte) (*ActRequest, *APIError) {
	var presence struct {
		Reference jsontext.Value `json:"reference"`
		Operation jsontext.Value `json:"operation"`
		Image     jsontext.Value `json:"image"`
	}
	var wire struct {
		Reference string        `json:"reference"`
		Operation *rawOperation `json:"operation"`
		Image     bool          `json:"image"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.Reference) == 0 || string(presence.Reference) == "null" {
		return nil, Invalid("act 需要 reference")
	}
	if len(presence.Operation) == 0 || string(presence.Operation) == "null" {
		return nil, Invalid("act 需要 operation 对象")
	}
	if err := checkNulls(map[string]jsontext.Value{"image": presence.Image}); err != nil {
		return nil, err
	}
	if err := checkReference(wire.Reference); err != nil {
		return nil, err
	}
	op, apiErr := parseOperation(wire.Operation)
	if apiErr != nil {
		return nil, apiErr
	}
	return &ActRequest{Reference: wire.Reference, Operation: *op, Image: wire.Image}, nil
}

type rawOperation struct {
	Type       string         `json:"type"`
	Target     optionalObject `json:"target"`
	From       optionalObject `json:"from"`
	To         optionalObject `json:"to"`
	Key        jsontext.Value `json:"key"`
	Text       jsontext.Value `json:"text"`
	Container  optionalObject `json:"container"`
	Direction  jsontext.Value `json:"direction"`
	Amount     jsontext.Value `json:"amount"`
	DurationMS jsontext.Value `json:"duration_ms"`
	Bubble     jsontext.Value `json:"bubble"`
	AutoSubmit jsontext.Value `json:"auto_submit"`
	SubmitRef  jsontext.Value `json:"submit_ref"`
}

type rawTarget struct {
	Ref jsontext.Value `json:"ref"`
	X   jsontext.Value `json:"x"`
	Y   jsontext.Value `json:"y"`
}

// optionalObject records whether an object-typed operation member appeared.
// Explicit null is a presence without an object so the branch mutex can reject
// it; nested unknown members are still rejected like the typed pass.
type optionalObject struct {
	Present bool
	Value   *rawTarget
}

// UnmarshalJSONFrom records member presence and decodes a non-null object.
func (o *optionalObject) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	o.Present = true
	if isJSONNull(raw) {
		return nil
	}
	var value rawTarget
	if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

func parseOperation(raw *rawOperation) (*Operation, *APIError) {
	if raw == nil {
		return nil, Invalid("operation 必须是对象")
	}
	op := &Operation{Type: raw.Type}
	hasKey := len(raw.Key) > 0
	hasText := len(raw.Text) > 0
	hasDirection := len(raw.Direction) > 0
	hasAmount := len(raw.Amount) > 0
	hasDuration := len(raw.DurationMS) > 0
	hasBubble := len(raw.Bubble) > 0
	hasAutoSubmit := len(raw.AutoSubmit) > 0
	hasSubmitRef := len(raw.SubmitRef) > 0
	bubble, bubblePresent, bubbleErr := parseOptionalBool("bubble", raw.Bubble)
	if bubbleErr != nil {
		return nil, bubbleErr
	}
	autoSubmit, autoSubmitPresent, autoSubmitErr := parseOptionalBool("auto_submit", raw.AutoSubmit)
	if autoSubmitErr != nil {
		return nil, autoSubmitErr
	}
	op.Bubble, op.HasBubble = bubble, bubblePresent
	op.AutoSubmit, op.HasAutoSubmit = autoSubmit, autoSubmitPresent
	if hasSubmitRef {
		if isJSONNull(raw.SubmitRef) {
			return nil, Invalid("submit_ref 不接受 null")
		}
		var submitRef string
		if err := json.Unmarshal(raw.SubmitRef, &submitRef); err != nil {
			return nil, Invalid("submit_ref 必须是字符串")
		}
		op.SubmitRef, op.HasSubmitRef = submitRef, true
	}
	switch raw.Type {
	case "tap", "long_press":
		if raw.Target.Value == nil {
			return nil, Invalid("该操作需要 target")
		}
		if raw.From.Present || raw.To.Present || hasKey || hasText ||
			raw.Container.Present || hasDirection || hasAmount || hasDuration ||
			hasAutoSubmit || hasSubmitRef {
			return nil, Invalid("该操作只接受 target 与按分支解释的 bubble")
		}
		target, err := parseTarget(raw.Target.Value)
		if err != nil {
			return nil, err
		}
		if hasBubble && !target.UseRef {
			return nil, Invalid("bubble 只适用于 ref 目标；坐标目标不接受 bubble")
		}
		op.Target = target
	case "text":
		if raw.Target.Value == nil {
			return nil, Invalid("text 操作需要 target")
		}
		if raw.From.Present || raw.To.Present || hasKey ||
			raw.Container.Present || hasDirection || hasAmount || hasDuration || hasBubble {
			return nil, Invalid("text 操作只接受 target、text 与可选的 auto_submit、submit_ref")
		}
		if !hasText || string(raw.Text) == "null" {
			return nil, Invalid("text 操作需要非 null 的 text")
		}
		var text string
		if err := json.Unmarshal(raw.Text, &text); err != nil {
			return nil, Invalid("text 必须是字符串")
		}
		if utf8.RuneCountInString(text) > MaxTextRunes {
			return nil, Invalid("text 超过长度上限").WithDetail("limit", MaxTextRunes)
		}
		target, err := parseTarget(raw.Target.Value)
		if err != nil {
			return nil, err
		}
		op.Target = target
		op.Text = text
	case "swipe":
		if raw.Target.Present || raw.Container.Present || hasKey || hasText ||
			hasDirection || hasAmount || hasBubble || hasAutoSubmit || hasSubmitRef {
			return nil, Invalid("swipe 只接受 from、to 与 duration_ms")
		}
		if raw.From.Value == nil || raw.To.Value == nil {
			if raw.From.Present || raw.To.Present {
				return nil, Invalid("swipe 的 from 与 to 不接受 null")
			}
			return nil, Invalid("swipe 需要 from 与 to")
		}
		from, err := parsePoint(raw.From.Value)
		if err != nil {
			return nil, err
		}
		to, err := parsePoint(raw.To.Value)
		if err != nil {
			return nil, err
		}
		if *from == *to {
			return nil, Invalid("swipe 起终点不能相同")
		}
		duration := DefaultSwipeDurationMS
		if hasDuration {
			duration, err = parseSwipeDuration(raw.DurationMS)
			if err != nil {
				return nil, err
			}
		}
		op.From, op.To, op.DurationMS = from, to, duration
	case "scroll":
		if raw.Target.Present || raw.From.Present || raw.To.Present ||
			hasKey || hasText || hasDuration || hasBubble || hasAutoSubmit || hasSubmitRef {
			return nil, Invalid("scroll 只接受 container、direction 与 amount")
		}
		if !raw.Container.Present {
			return nil, Invalid("scroll 操作需要 container")
		}
		if raw.Container.Value == nil {
			return nil, Invalid("container 不接受 null")
		}
		container, err := parseTarget(raw.Container.Value)
		if err != nil {
			return nil, err
		}
		if !hasDirection {
			return nil, Invalid("scroll 操作需要 direction")
		}
		direction, err := parseScrollDirection(raw.Direction)
		if err != nil {
			return nil, err
		}
		amount := ScrollAmountPage
		if hasAmount {
			amount, err = parseScrollAmount(raw.Amount)
			if err != nil {
				return nil, err
			}
		}
		op.Container, op.Direction, op.Amount = container, direction, amount
	case "key":
		if raw.Target.Present || raw.From.Present || raw.To.Present || hasText ||
			raw.Container.Present || hasDirection || hasAmount || hasDuration ||
			hasBubble || hasAutoSubmit || hasSubmitRef {
			return nil, Invalid("key 操作只接受 key")
		}
		if !hasKey || string(raw.Key) == "null" {
			return nil, Invalid("key 操作需要 key")
		}
		var key string
		if err := json.Unmarshal(raw.Key, &key); err != nil {
			return nil, Invalid("key 必须是字符串")
		}
		if !Keys[key] {
			return nil, NewError(Status(CodeInvalidKey), CodeInvalidKey, "不支持的按键")
		}
		op.Key = key
	default:
		return nil, Invalid("不支持的 operation.type")
	}
	if apiErr := op.ValidateEffectiveDefaults(); apiErr != nil {
		return nil, apiErr
	}
	return op, nil
}

// parseOptionalBool records both the decoded value and whether the member was
// present, so an omitted member can never be confused with an explicit false.
// Explicit null and non-boolean values are rejected before any branch logic.
func parseOptionalBool(name string, raw jsontext.Value) (bool, bool, *APIError) {
	if len(raw) == 0 {
		return false, false, nil
	}
	if isJSONNull(raw) {
		return false, false, Invalid(name + " 不接受 null")
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false, Invalid(name + " 必须是布尔值")
	}
	return value, true, nil
}

// parseScrollDirection admits exactly the four declared lowercase directions.
func parseScrollDirection(raw jsontext.Value) (string, *APIError) {
	if isJSONNull(raw) {
		return "", Invalid("direction 不接受 null")
	}
	var direction string
	if err := json.Unmarshal(raw, &direction); err != nil {
		return "", Invalid("direction 必须是字符串")
	}
	switch direction {
	case ScrollDirectionUp, ScrollDirectionDown, ScrollDirectionLeft, ScrollDirectionRight:
		return direction, nil
	default:
		return "", Invalid("direction 只接受 up、down、left、right")
	}
}

// parseScrollAmount admits the bounded page/half travel amounts.
func parseScrollAmount(raw jsontext.Value) (string, *APIError) {
	if isJSONNull(raw) {
		return "", Invalid("amount 不接受 null")
	}
	var amount string
	if err := json.Unmarshal(raw, &amount); err != nil {
		return "", Invalid("amount 必须是字符串")
	}
	switch amount {
	case ScrollAmountPage, ScrollAmountHalf:
		return amount, nil
	default:
		return "", Invalid("amount 只接受 page 或 half")
	}
}

// parseSwipeDuration admits one integer duration inside the fixed bounds.
func parseSwipeDuration(raw jsontext.Value) (int, *APIError) {
	if isJSONNull(raw) {
		return 0, Invalid("duration_ms 不接受 null")
	}
	var duration int
	if err := json.Unmarshal(raw, &duration); err != nil {
		return 0, Invalid("duration_ms 必须是整数")
	}
	if duration < MinSwipeDurationMS || duration > MaxSwipeDurationMS {
		return 0, Invalid("duration_ms 超出允许范围").WithDetail("limit_ms", MaxSwipeDurationMS)
	}
	return duration, nil
}

func parseTarget(raw *rawTarget) (*Target, *APIError) {
	hasRef := len(raw.Ref) > 0
	hasX := len(raw.X) > 0
	hasY := len(raw.Y) > 0
	switch {
	case hasRef && (hasX || hasY):
		return nil, Invalid("target 的 ref 与坐标互斥")
	case hasX != hasY:
		return nil, Invalid("target 坐标需要同时提供 x 与 y")
	case hasRef:
		if string(raw.Ref) == "null" {
			return nil, Invalid("ref 不接受 null")
		}
		var ref string
		if err := json.Unmarshal(raw.Ref, &ref); err != nil {
			return nil, Invalid("target.ref 必须是字符串")
		}
		if ref == "" {
			return nil, Invalid("target.ref 不能为空")
		}
		return &Target{UseRef: true, Ref: ref}, nil
	case hasX && hasY:
		point, err := parsePoint(raw)
		if err != nil {
			return nil, err
		}
		return &Target{X: point.X, Y: point.Y}, nil
	default:
		return nil, Invalid("target 需要 ref 或 x/y")
	}
}

func parsePoint(raw *rawTarget) (*Point, *APIError) {
	if len(raw.Ref) > 0 {
		return nil, Invalid("坐标对象不接受 ref")
	}
	if len(raw.X) == 0 || len(raw.Y) == 0 {
		return nil, Invalid("坐标需要同时提供 x 与 y")
	}
	if string(raw.X) == "null" || string(raw.Y) == "null" {
		return nil, Invalid("坐标不接受 null")
	}
	var x, y int
	if err := json.Unmarshal(raw.X, &x); err != nil {
		return nil, Invalid("坐标必须是整数")
	}
	if err := json.Unmarshal(raw.Y, &y); err != nil {
		return nil, Invalid("坐标必须是整数")
	}
	if x < 0 || y < 0 {
		return nil, Invalid("坐标不能为负")
	}
	return &Point{X: x, Y: y}, nil
}

func checkReference(ref string) *APIError {
	if ref == "" {
		return Invalid("编号不能为空")
	}
	if len(ref) > MaxReferenceLen {
		return Invalid("编号长度超限")
	}
	if ref[0] != 'O' && ref[0] != 'P' {
		return Invalid("编号类型无效")
	}
	return nil
}

func checkNulls(fields map[string]jsontext.Value) *APIError {
	for _, raw := range fields {
		if len(raw) > 0 && string(raw) == "null" {
			return Invalid("参数不接受显式 null；省略该字段使用默认值")
		}
	}
	return nil
}

// decodeStrict runs the presence pass and the typed pass. The presence type
// must list every admitted member; unknown members and duplicate names are
// rejected by the first pass.
func decodeStrict(body []byte, presence, out any) error {
	if len(body) == 0 {
		return errors.New("empty body")
	}
	for _, b := range body {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{':
		default:
			return errors.New("request body must be a JSON object")
		}
		break
	}
	if err := json.Unmarshal(body, presence, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	return json.Unmarshal(body, out, json.RejectUnknownMembers(true))
}

func invalidDecode(err error) *APIError {
	return Invalid("请求参数不符合工具契约")
}
