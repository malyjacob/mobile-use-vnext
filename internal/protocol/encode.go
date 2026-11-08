package protocol

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
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

// Encode writes the canonical act(observation_id, operation, image) body.
func (r *ActRequest) Encode() ([]byte, error) {
	if r.ObservationID == "" {
		return nil, errors.New("act requires an observation id")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "observation_id"); err != nil {
			return err
		}
		if err := writeString(enc, r.ObservationID); err != nil {
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
		if err := writeTargetOrPoint(enc, "target", op.Target, nil); err != nil {
			return err
		}
	case "text":
		if err := writeTargetOrPoint(enc, "target", op.Target, nil); err != nil {
			return err
		}
		if err := writeName(enc, "text"); err != nil {
			return err
		}
		if err := writeString(enc, op.Text); err != nil {
			return err
		}
	case "swipe":
		if op.From == nil || op.To == nil {
			return errors.New("swipe requires from and to")
		}
		if err := writePointField(enc, "from", op.From); err != nil {
			return err
		}
		if err := writePointField(enc, "to", op.To); err != nil {
			return err
		}
	case "key":
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

// Encode writes the canonical inspect(observation_id, node, query) body.
func (r *InspectRequest) Encode() ([]byte, error) {
	if r.ObservationID == "" {
		return nil, errors.New("inspect requires an observation id")
	}
	if r.HasNode && r.HasQuery {
		return nil, errors.New("inspect node and query are mutually exclusive")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "observation_id"); err != nil {
			return err
		}
		if err := writeString(enc, r.ObservationID); err != nil {
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

// Encode writes the canonical keepalive(reference) body.
func (r *KeepaliveRequest) Encode() ([]byte, error) {
	if r.Reference == "" {
		return nil, errors.New("keepalive requires a reference")
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

// Encode writes the canonical apps(query=None) body.
func (r *AppsRequest) Encode() ([]byte, error) {
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
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
		return enc.WriteToken(jsontext.EndObject)
	})
}

// Encode writes the canonical open_app(observation_id, app) body.
func (r *OpenAppRequest) Encode() ([]byte, error) {
	if r.ObservationID == "" || r.App == "" {
		return nil, errors.New("open_app requires observation_id and app")
	}
	return encodeRequest(func(enc *jsontext.Encoder) error {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := writeName(enc, "observation_id"); err != nil {
			return err
		}
		if err := writeString(enc, r.ObservationID); err != nil {
			return err
		}
		if err := writeName(enc, "app"); err != nil {
			return err
		}
		if err := writeString(enc, r.App); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	})
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
