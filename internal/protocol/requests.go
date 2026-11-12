package protocol

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"unicode/utf8"
)

// The maximum sizes are fixed internal policy; callers set no timeout/TTL.
const (
	MaxTextRunes    = 4096
	MaxQueryRunes   = 256
	MaxCommandRunes = 4096
	MaxWaitMS       = 10000
	MaxReferenceLen = 64
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
}

// Point is one absolute screen coordinate.
type Point struct{ X, Y int }

// ActRequest is act(observation_id, operation, image=false).
type ActRequest struct {
	ObservationID string
	Operation     Operation
	Image         bool
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

// DecodeAct admits act(observation_id, operation, image).
func DecodeAct(body []byte) (*ActRequest, *APIError) {
	var presence struct {
		ObservationID jsontext.Value `json:"observation_id"`
		Operation     jsontext.Value `json:"operation"`
		Image         jsontext.Value `json:"image"`
	}
	var wire struct {
		ObservationID string        `json:"observation_id"`
		Operation     *rawOperation `json:"operation"`
		Image         bool          `json:"image"`
	}
	if err := decodeStrict(body, &presence, &wire); err != nil {
		return nil, invalidDecode(err)
	}
	if len(presence.ObservationID) == 0 || string(presence.ObservationID) == "null" {
		return nil, Invalid("act 需要 observation_id")
	}
	if len(presence.Operation) == 0 || string(presence.Operation) == "null" {
		return nil, Invalid("act 需要 operation 对象")
	}
	if err := checkNulls(map[string]jsontext.Value{"image": presence.Image}); err != nil {
		return nil, err
	}
	if err := checkReference(wire.ObservationID); err != nil {
		return nil, err
	}
	op, apiErr := parseOperation(wire.Operation)
	if apiErr != nil {
		return nil, apiErr
	}
	return &ActRequest{ObservationID: wire.ObservationID, Operation: *op, Image: wire.Image}, nil
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
	switch raw.Type {
	case "tap", "long_press":
		if raw.Target.Value == nil {
			return nil, Invalid("该操作需要 target")
		}
		if raw.From.Present || raw.To.Present || hasKey || hasText ||
			raw.Container.Present || hasDirection || hasAmount || hasDuration {
			return nil, Invalid("该操作只接受 target")
		}
		target, err := parseTarget(raw.Target.Value)
		if err != nil {
			return nil, err
		}
		op.Target = target
	case "text":
		if raw.Target.Value == nil {
			return nil, Invalid("text 操作需要 target")
		}
		if raw.From.Present || raw.To.Present || hasKey ||
			raw.Container.Present || hasDirection || hasAmount || hasDuration {
			return nil, Invalid("text 操作只接受 target 与 text")
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
			hasDirection || hasAmount {
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
			hasKey || hasText || hasDuration {
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
			raw.Container.Present || hasDirection || hasAmount || hasDuration {
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
	return op, nil
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
