package mcp

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// canonicalResult owns the adapter projection. Never mutate the service result,
// including its nested error/details or transport image metadata.
func canonicalResult(e *execution) (json.RawMessage, error) {
	r := protocol.Result{}
	if e.result != nil {
		r = *e.result
	}
	if r.Observation != nil && r.Observation.Image != nil {
		o := *r.Observation
		i := *o.Image
		i.Base64 = ""
		o.Image = &i
		r.Observation = &o
	}
	if e.cancelled {
		err := protocol.Error{Code: "call_cancelled", Message: e.cancellation}
		if r.Error != nil {
			err = *r.Error
		}
		details := make(map[string]any, len(err.Details)+2)
		for k, v := range err.Details {
			details[k] = v
		}
		details["mcp.cancelled"] = true
		details["mcp.cancellation"] = e.cancellation
		err.Details = details
		r.Error = &err
	}
	return jsonv2.Marshal(&r, jsonv2.Deterministic(true))
}

var encodingFailureJSON = json.RawMessage(`{"error":{"code":"internal_error","message":"结果编码失败"}}`)
