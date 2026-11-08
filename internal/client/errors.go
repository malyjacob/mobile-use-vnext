package client

import (
	"strconv"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Adapter error codes describe failures where no usable service result can be
// projected; service error codes are passed through unchanged.
const (
	// ErrorTransport reports a dial, write, read, timeout or context failure.
	ErrorTransport = "transport_unavailable"
	// ErrorResponse reports an unparseable, wrong-shaped or incomplete
	// service response, including malformed image bytes or metadata.
	ErrorResponse = "invalid_response"
	// ErrorTooLarge reports a response body over the adapter budget.
	ErrorTooLarge = "response_too_large"
)

// Controlled adapter messages. They never contain a URL, credential, request
// body, device output or a raw Go error string.
const (
	messageTransport        = "服务请求未完成"
	messageInvalidResponse  = "服务响应不符合契约"
	messageTooLarge         = "服务响应超过大小上限"
	messageInvalidOperation = "操作编号不符合契约"
	messageOperationID      = "操作编号生成失败"
	messageMissingRequest   = "请求参数不能为空"
	messageInvalidRequest   = "请求参数不符合工具契约"
	messageMissingContext   = "缺少调用上下文"
	messageHealthStatus     = "服务未就绪"
	messageHealthHTTP       = "服务健康检查失败"
	messageContractMismatch = "服务契约版本不匹配"
)

// Error is the typed call error. It carries the operation ID retained from the
// request, the stable service or adapter code, the HTTP status when one was
// received, and the structured result when the service produced one. Its text
// is deterministic and controlled; it never includes transport details.
type Error struct {
	OperationID string           // P retained even when the response was lost
	Code        string           // service code, or one of the adapter codes above
	Message     string           // controlled short sentence
	HTTPStatus  int              // 0 when no response was received
	Transport   bool             // true only for the adapter transport code
	Result      *protocol.Result // present when structured result JSON was decoded
}

// Error implements error with code, message and HTTP status only.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	text := e.Code
	if text == "" {
		text = "client_error"
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	if e.HTTPStatus != 0 {
		text += " (HTTP " + strconv.Itoa(e.HTTPStatus) + ")"
	}
	return text
}

// invalidRequest builds a caller-side error without contacting the service.
func invalidRequest(operationID, message string) *Error {
	return &Error{
		OperationID: operationID,
		Code:        protocol.CodeInvalidRequest,
		Message:     message,
	}
}

// transportFailure builds the fixed, non-identifying transport error.
func transportFailure(operationID string) *Error {
	return &Error{
		OperationID: operationID,
		Code:        ErrorTransport,
		Message:     messageTransport,
		Transport:   true,
	}
}
