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
	Type   string         `json:"type"`
	Target *rawTarget     `json:"target"`
	From   *rawTarget     `json:"from"`
	To     *rawTarget     `json:"to"`
	Key    jsontext.Value `json:"key"`
	Text   jsontext.Value `json:"text"`
}

type rawTarget struct {
	Ref jsontext.Value `json:"ref"`
	X   jsontext.Value `json:"x"`
	Y   jsontext.Value `json:"y"`
}

func parseOperation(raw *rawOperation) (*Operation, *APIError) {
	if raw == nil {
		return nil, Invalid("operation 必须是对象")
	}
	op := &Operation{Type: raw.Type}
	hasKey := len(raw.Key) > 0
	hasText := len(raw.Text) > 0
	switch raw.Type {
	case "tap", "long_press":
		if raw.Target == nil {
			return nil, Invalid("该操作需要 target")
		}
		if raw.From != nil || raw.To != nil || hasKey || hasText {
			return nil, Invalid("该操作只接受 target")
		}
		target, err := parseTarget(raw.Target)
		if err != nil {
			return nil, err
		}
		op.Target = target
	case "text":
		if raw.Target == nil {
			return nil, Invalid("text 操作需要 target")
		}
		if raw.From != nil || raw.To != nil || hasKey {
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
		target, err := parseTarget(raw.Target)
		if err != nil {
			return nil, err
		}
		op.Target = target
		op.Text = text
	case "swipe":
		if raw.Target != nil || hasKey || hasText {
			return nil, Invalid("swipe 只接受 from 与 to")
		}
		if raw.From == nil || raw.To == nil {
			return nil, Invalid("swipe 需要 from 与 to")
		}
		from, err := parsePoint(raw.From)
		if err != nil {
			return nil, err
		}
		to, err := parsePoint(raw.To)
		if err != nil {
			return nil, err
		}
		if *from == *to {
			return nil, Invalid("swipe 起终点不能相同")
		}
		op.From, op.To = from, to
	case "key":
		if raw.Target != nil || raw.From != nil || raw.To != nil || hasText {
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
