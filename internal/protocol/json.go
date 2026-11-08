// Package protocol holds the public data model, strict request admission and
// the stable error vocabulary shared by the service, HTTP clients and (later)
// the MCP adapter. It does not import any business implementation package.
package protocol

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strings"
)

// JSON returns the wire encoding of v. It is used at the HTTP boundary and in
// tests; callers must not add a second business projection around it.
func JSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// MarshalJSONTo encodes v into an existing encoder.
func MarshalJSONTo(enc *jsontext.Encoder, v any) error {
	return json.MarshalEncode(enc, v)
}

// Decode parses one JSON value strictly: duplicate object member names,
// unknown members and trailing values are rejected by encoding/json/v2.
func Decode(data []byte, v any) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return errors.New("empty JSON body")
	}
	return json.Unmarshal(data, v, json.RejectUnknownMembers(true))
}

// ReadValue reads exactly one raw JSON value from dec.
func ReadValue(dec *jsontext.Decoder) (jsontext.Value, error) {
	value, err := dec.ReadValue()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, err
		}
		return nil, err
	}
	return value, nil
}

// writeNull writes a JSON null token.
func writeNull(enc *jsontext.Encoder) error { return enc.WriteToken(jsontext.Null) }

// writeBool writes a JSON boolean token.
func writeBool(enc *jsontext.Encoder, v bool) error { return enc.WriteToken(jsontext.Bool(v)) }

// writeString writes a JSON string token.
func writeString(enc *jsontext.Encoder, v string) error { return enc.WriteToken(jsontext.String(v)) }

// writeName writes an object member name.
func writeName(enc *jsontext.Encoder, name string) error {
	return enc.WriteToken(jsontext.String(name))
}

// writeInt writes a JSON integer token.
func writeInt(enc *jsontext.Encoder, v int) error { return enc.WriteToken(jsontext.Int(int64(v))) }

var errUnknownMember = errors.New("unknown object member")

// rejectKind builds a controlled decoding error for an unexpected JSON kind.
func rejectKind(what string) error {
	return fmt.Errorf("invalid JSON value for %s", what)
}
