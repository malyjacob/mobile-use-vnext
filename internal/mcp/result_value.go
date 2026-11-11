package mcp

import (
	"bytes"
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
		if e.appKind == protocol.AppKindFind || e.appKind == protocol.AppKindList {
			// A cancelled query has no outcome and no recovery step: the
			// adapter never issued an end, and a finished query P must not be
			// pointed back at status as if it could advance. A known service
			// error keeps its code/message/details above; known collections
			// and other business facts stay untouched.
			r.Outcome = ""
			r.Recovery = nil
		}
	}
	value, err := jsonv2.Marshal(&r, jsonv2.Deterministic(true))
	if err != nil {
		return nil, err
	}
	// AppView's manual marshaler always emits find/list items and total. The
	// wire presence of the decoded view decides whether a collection was
	// actually obtained: nil Items/TaskItems without HasTotal means the service
	// reported only kind/metadata (a failed or summary query, or the adapter's
	// own kind-only call metadata), and the canonical A must not fabricate
	// items:[] or total:0.
	if r.App != nil {
		strip := false
		switch r.App.Kind {
		case protocol.AppKindFind:
			strip = r.App.Items == nil && !r.App.HasTotal
		case protocol.AppKindList:
			strip = r.App.TaskItems == nil && !r.App.HasTotal
		}
		if strip {
			return stripAppCollectionMembers(value)
		}
	}
	return value, nil
}

// stripAppCollectionMembers removes only app.items and app.total from an
// already canonical Result document, preserving every other member and its
// wire order. It never adds a fact.
func stripAppCollectionMembers(raw []byte) (json.RawMessage, error) {
	members, err := resultMembers(raw)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for index, member := range members {
		if index > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(member.name)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		if member.name != "app" {
			out.Write(member.value)
			continue
		}
		appMembers, err := resultMembers(member.value)
		if err != nil {
			return nil, err
		}
		out.WriteByte('{')
		written := 0
		for _, appMember := range appMembers {
			if appMember.name == "items" || appMember.name == "total" {
				continue
			}
			if written > 0 {
				out.WriteByte(',')
			}
			appKey, err := json.Marshal(appMember.name)
			if err != nil {
				return nil, err
			}
			out.Write(appKey)
			out.WriteByte(':')
			out.Write(appMember.value)
			written++
		}
		out.WriteByte('}')
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

var encodingFailureJSON = json.RawMessage(`{"error":{"code":"internal_error","message":"结果编码失败"}}`)
