package mcp

import (
	"errors"
	"net/http"
	"strings"
)

// ResultMode selects how one MCP result is projected to the client.
type ResultMode uint8

const (
	// ResultModeBoth emits complete text and structured result forms.
	ResultModeBoth ResultMode = iota
	// ResultModeText emits only the complete text result form.
	ResultModeText
	// ResultModeStructured emits only the structured result form.
	ResultModeStructured
)

const (
	// ResultModeHeader is the HTTP request header that selects a result mode.
	ResultModeHeader = "X-Mobile-Use-Result-Mode"
	// ResultModeEnv is the stdio default result mode environment variable.
	ResultModeEnv = "MOBILE_USE_MCP_RESULT_MODE"
)

var (
	errInvalidResultMode       = errors.New("result mode must be both, text, or structured")
	errInvalidResultModeHeader = errors.New("result mode header must contain one exact mode value")
)

// ParseResultMode parses one exact, case-sensitive result mode value.
func ParseResultMode(value string) (ResultMode, error) {
	switch value {
	case "both":
		return ResultModeBoth, nil
	case "text":
		return ResultModeText, nil
	case "structured":
		return ResultModeStructured, nil
	default:
		return ResultModeBoth, errInvalidResultMode
	}
}

// String returns the stable enum token used in startup logs.
func (m ResultMode) String() string {
	switch m {
	case ResultModeBoth:
		return "both"
	case ResultModeText:
		return "text"
	case ResultModeStructured:
		return "structured"
	default:
		return "unknown"
	}
}

// ResultModeFromHeader selects a request mode from header. An absent header
// uses defaultMode; a present header must have exactly one exact mode value.
// Callers can pass nil for stdio requests.
func ResultModeFromHeader(header http.Header, defaultMode ResultMode) (ResultMode, error) {
	if !defaultMode.valid() {
		return ResultModeBoth, errInvalidResultMode
	}
	values := header.Values(ResultModeHeader)
	if len(values) == 0 {
		return defaultMode, nil
	}
	if len(values) != 1 {
		return ResultModeBoth, errInvalidResultModeHeader
	}
	value := values[0]
	if value == "" || strings.TrimSpace(value) != value || strings.Contains(value, ",") {
		return ResultModeBoth, errInvalidResultModeHeader
	}
	return ParseResultMode(value)
}

func (m ResultMode) valid() bool {
	return m == ResultModeBoth || m == ResultModeText || m == ResultModeStructured
}
