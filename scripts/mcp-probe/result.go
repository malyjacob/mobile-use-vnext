package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"strconv"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The probe compares stable public values only.
const (
	outcomeExecuted     = "executed"
	sessionActive       = "active"
	sessionStarting     = "starting"
	sessionEnded        = "ended"
	observationComplete = "complete"
	observationPartial  = "partial"
	operationFinished   = "finished"
	operationRunning    = "running"
	operationNotSeen    = "not_seen"
	requestClosedLabel  = "request_closed"
	treeFull            = "full"
	treeDelta           = "delta"
	imageJPEG           = "image/jpeg"
)

// sessionView is the public session projection of one structured result.
type sessionView struct {
	Origin    string `json:"origin"`
	State     string `json:"state"`
	Mode      string `json:"mode"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
	Reason    string `json:"reason"`
}

// errorView is the controlled error object; only the stable code is read.
type errorView struct {
	Code string `json:"code"`
}

// imageView is the observation image metadata carried by structuredContent.
type imageView struct {
	CapturedAt string     `json:"captured_at"`
	Width      int        `json:"width"`
	Height     int        `json:"height"`
	MediaType  string     `json:"media_type"`
	ReadOnly   bool       `json:"read_only"`
	Error      *errorView `json:"error"`
}

// treeView keeps only the tree format and delta base.
type treeView struct {
	Format string `json:"format"`
	Base   string `json:"base"`
}

// observationView is the observation header; tree bodies are not interpreted.
type observationView struct {
	Status   string     `json:"status"`
	ID       string     `json:"id"`
	Origin   string     `json:"origin"`
	Revision int        `json:"revision"`
	Missing  []string   `json:"missing"`
	Tree     *treeView  `json:"tree"`
	Image    *imageView `json:"image"`
	Error    *errorView `json:"error"`
}

type cleanupView struct {
	State     string `json:"state"`
	Execution string `json:"execution"`
	Tasks     string `json:"tasks"`
	Recents   string `json:"recents"`
	Display   string `json:"display"`
}

// resultView is the probe's read-only projection of structuredContent.
type resultView struct {
	OperationID      string           `json:"operation_id"`
	OperationState   string           `json:"operation_state"`
	Outcome          string           `json:"outcome"`
	Phase            string           `json:"phase"`
	TargetReference  string           `json:"target_reference"`
	RequestClosed    *bool            `json:"request_closed"`
	CanStart         *bool            `json:"can_start"`
	BlockingCode     string           `json:"blocking_code"`
	StartOperation   string           `json:"start_operation_id"`
	ObservationState string           `json:"observation_state"`
	Session          *sessionView     `json:"session"`
	Observation      *observationView `json:"observation"`
	Cleanup          *cleanupView     `json:"cleanup"`
	Error            *errorView       `json:"error"`
}

// checkError is one failed assertion with controlled reason and fields.
type checkError struct {
	reason string
	fields []string
}

func failf(reason string, fields ...string) *checkError {
	return &checkError{reason: reason, fields: fields}
}

// callResult is one projected tool result. structured is the canonical
// structuredContent JSON, kept only for leak scanning.
type callResult struct {
	result     *sdk.CallToolResult
	view       resultView
	structured []byte
	texts      []string
	images     []*sdk.ImageContent
}

// project decodes a tool result into the probe's projection without printing
// any content.
func project(result *sdk.CallToolResult) (*callResult, *checkError) {
	if result == nil {
		return nil, failf("empty_result")
	}
	projected := &callResult{result: result}
	for _, content := range result.Content {
		switch value := content.(type) {
		case *sdk.TextContent:
			projected.texts = append(projected.texts, value.Text)
		case *sdk.ImageContent:
			projected.images = append(projected.images, value)
		}
	}
	if result.StructuredContent != nil {
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return nil, failf("structured_content_unusable")
		}
		projected.structured = encoded
		if err := json.Unmarshal(encoded, &projected.view); err != nil {
			return nil, failf("structured_content_unusable")
		}
	}
	return projected, nil
}

func codeOf(view resultView) string {
	if view.Error == nil {
		return "none"
	}
	return view.Error.Code
}

func treeFormatOf(observation *observationView) string {
	if observation == nil || observation.Tree == nil || observation.Tree.Format == "" {
		return "none"
	}
	return observation.Tree.Format
}

func sessionStateOf(session *sessionView) string {
	if session == nil {
		return "none"
	}
	return session.State
}

// validReference accepts the public P and O identifiers: one prefix byte and
// at most 63 further ASCII letters, digits, hyphen or underscore.
func validReference(value string, prefix byte) bool {
	if len(value) < 2 || len(value) > 64 || value[0] != prefix {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-', character == '_':
		default:
			return false
		}
	}
	return true
}

// jpegMagic reports whether the bytes start with a JPEG SOI and marker.
func jpegMagic(data []byte) bool {
	return len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF
}

// imageBytes returns the decoded image bytes of the first ImageContent. The
// SDK decodes the base64 carrier into Data; a still-encoded carrier is
// accepted defensively.
func imageBytes(projected *callResult, observation *observationView) ([]byte, *checkError) {
	if len(projected.images) == 0 {
		fields := []string{}
		if observation != nil && observation.Image != nil && observation.Image.Error != nil {
			fields = append(fields, field("image_error", observation.Image.Error.Code))
		}
		return nil, failf("image_content_missing", fields...)
	}
	image := projected.images[0]
	if image.MIMEType != imageJPEG {
		return nil, failf("image_mime_unexpected", field("mime", image.MIMEType))
	}
	raw := image.Data
	if len(raw) == 0 {
		return nil, failf("image_data_empty")
	}
	if !jpegMagic(raw) {
		decoded, err := base64.StdEncoding.DecodeString(string(raw))
		if err != nil {
			return nil, failf("image_not_jpeg")
		}
		raw = decoded
	}
	if !jpegMagic(raw) {
		return nil, failf("image_not_jpeg")
	}
	if len(raw) <= 1024 {
		return nil, failf("image_too_small", field("image_bytes", strconv.Itoa(len(raw))))
	}
	if observation == nil || observation.Image == nil {
		return nil, failf("image_metadata_missing")
	}
	meta := observation.Image
	if meta.Width <= 0 || meta.Height <= 0 {
		return nil, failf("image_metadata_dimensions",
			field("width", strconv.Itoa(meta.Width)), field("height", strconv.Itoa(meta.Height)))
	}
	if meta.MediaType != imageJPEG {
		return nil, failf("image_metadata_mime", field("media_type", meta.MediaType))
	}
	if meta.CapturedAt == "" {
		return nil, failf("image_metadata_missing_captured_at")
	}
	return raw, nil
}

// checkNoImageCarrier asserts the text projection and structuredContent carry
// neither the base64 member nor the exact base64 payload of this image.
func checkNoImageCarrier(projected *callResult, raw []byte) *checkError {
	payload := base64.StdEncoding.EncodeToString(raw)
	for _, text := range projected.texts {
		if strings.Contains(text, "base64") {
			return failf("text_carries_base64")
		}
		if strings.Contains(text, payload) {
			return failf("text_carries_image_bytes")
		}
	}
	if len(projected.structured) > 0 {
		if bytes.Contains(projected.structured, []byte(`"base64"`)) {
			return failf("structured_carries_base64")
		}
		if bytes.Contains(projected.structured, []byte(payload)) {
			return failf("structured_carries_image_bytes")
		}
	}
	return nil
}
