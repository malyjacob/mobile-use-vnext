package protocol

import (
	"errors"
	"net/http"
)

// APIError is the controlled error shape shared across HTTP and later MCP.
// Code is stable; Message is a short controlled sentence. Details may only
// carry whitelisted phase/check/limit facts, never user text or device output.
type APIError struct {
	Code       string
	Message    string
	Details    map[string]any
	HTTPStatus int
	Outcome    string // not_executed / uncertain / "" (no active action)
	Next       string // status / observe / end / user / update
}

// Error implements error.
func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Code + ": " + e.Message
}

// Public converts the error to its wire shape.
func (e *APIError) Public() *Error {
	if e == nil {
		return nil
	}
	return &Error{Code: e.Code, Message: e.Message, Details: e.Details}
}

// Error is the public controlled error object.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// NewError builds an APIError with the given status and outcome.
func NewError(status int, code, message string) *APIError {
	return &APIError{Code: code, Message: message, HTTPStatus: status, Next: nextFor(code)}
}

// NewErrorOutcome builds an APIError that also states the action outcome.
func NewErrorOutcome(status int, code, message, outcome string) *APIError {
	e := NewError(status, code, message)
	e.Outcome = outcome
	return e
}

// NextForCode returns the controlled recovery entry point for a code.
func NextForCode(code string) string { return nextFor(code) }

func nextFor(code string) string {
	switch code {
	case "device_busy", "operation_running", "delivery_unavailable",
		"app_launch_unconfirmed", "application_moved", "task_focus_unavailable",
		"shell_result_unconfirmed", "task_remove_unconfirmed",
		"app_list_unavailable":
		return "status"
	case "observation_expired", "observation_retired", "observation_claimed",
		"observation_not_ready", "no_ready_observation", "tree_unavailable",
		"content_unavailable":
		return "observe"
	case "session_ended", "session_expired", "control_revoked":
		return "end"
	case "device_locked", "device_not_interactive", "stop_unavailable",
		"mode_not_selected", "control_unavailable", "app_not_found",
		"root_unavailable":
		return "user"
	case "contract_mismatch":
		return "update"
	}
	return ""
}

// Stable error codes used by the first implemented tool subset.
const (
	CodeInvalidRequest     = "invalid_request"
	CodeUnauthorized       = "unauthorized"
	CodeContractMismatch   = "contract_mismatch"
	CodeDeviceBusy         = "device_busy"
	CodeDeviceUnavailable  = "device_unavailable"
	CodeControlUnavailable = "control_unavailable"
	CodeStopUnavailable    = "stop_unavailable"
	CodeDeviceLocked       = "device_locked"
	CodeDeviceNotAwake     = "device_not_interactive"
	CodeModeNotSelected    = "mode_not_selected"
	CodeRecovering         = "recovering"
	CodeSessionExists      = "session_exists"
	CodeSessionNotFound    = "session_not_found"
	CodeSessionEnded       = "session_ended"
	CodeSessionExpired     = "session_expired"
	CodeReferenceInvalid   = "reference_invalid"
	CodeReferenceNoSession = "reference_has_no_session"
	CodeOperationNotFound  = "operation_not_found"
	CodeOperationUnknown   = "operation_unknown"
	CodeObservationGone    = "observation_not_found"
	CodeObservationExpired = "observation_expired"
	CodeObservationClaimed = "observation_claimed"
	CodeObservationRetired = "observation_retired"
	CodeNoReadyObservation = "no_ready_observation"
	CodeNodeNotFound       = "node_not_found"
	CodeNodeDisabled       = "node_disabled"
	CodeNodeUnsupported    = "unsupported_node_action"
	CodeInvalidCoordinates = "invalid_coordinates"
	CodeInvalidKey         = "unsupported_key"
	CodeTextTooLong        = "text_too_long"
	CodeTextTargetUnknown  = "text_target_uncertain"
	CodeTextResultUnknown  = "text_result_uncertain"
	CodeTargetChanged      = "target_changed"
	CodeForegroundChanged  = "foreground_changed"
	CodeForegroundUnknown  = "foreground_unknown"
	CodeControlUI          = "control_ui_on_virtual_display"
	CodeActionUnknown      = "action_result_uncertain"
	CodeDisplayUnavailable = "display_unavailable"
	CodeDisplayCreate      = "display_create_failed"
	CodeHierarchyFailed    = "hierarchy_unavailable"
	CodeTreeTooLarge       = "tree_too_large"
	CodeStaleScreen        = "stale_screen"
	CodeStopped            = "control_revoked"
	CodeRequestClosed      = "request_closed"
	CodeCleanupUnconfirmed = "cleanup_unconfirmed"
	CodeUnsupported        = "unsupported_feature"
	CodeInternal           = "internal_error"
	CodeForbidden          = "forbidden"

	// M4 tool-surface codes. These describe controlled stages of the
	// discovery, app launch and managed shell tools; no new code changes the
	// meaning of the P3 codes above.
	CodeAppNotFound             = "app_not_found"
	CodeAppLaunchFailed         = "app_launch_failed"
	CodeAppLaunchUnknown        = "app_launch_unconfirmed"
	CodeAppMoved                = "application_moved"
	CodeAppListUnavailable      = "app_list_unavailable"
	CodeAppTaskUnconfirmed      = "task_remove_unconfirmed"
	CodeTaskFocus               = "task_focus_unavailable"
	CodeContentUnavailable      = "content_unavailable"
	CodeInspectionTooLarge      = "inspection_too_large"
	CodeShellUnknown            = "shell_result_unconfirmed"
	CodeShellStartFailed        = "shell_start_failed"
	CodeShellOutput             = "shell_output_unavailable"
	CodeRootUnavailable         = "root_unavailable"
	CodeDeliveryUnavailable     = "delivery_unavailable"
	CodeNotificationUnavailable = "notification_unavailable"
)

// Err is a sentinel used by the engine to signal that the work position is
// occupied. The caller maps it to a busy error with its own operation ID.
var ErrBusy = errors.New("device work position is occupied")

// Status returns the HTTP status for a code, defaulting to 500.
func Status(code string) int {
	switch code {
	case CodeInvalidRequest, CodeReferenceInvalid, CodeInvalidCoordinates,
		CodeInvalidKey, CodeTextTooLong, CodeNodeUnsupported:
		return http.StatusUnprocessableEntity
	case CodeUnauthorized, CodeForbidden:
		return http.StatusUnauthorized
	case CodeContractMismatch, CodeDeviceBusy, CodeControlUnavailable,
		CodeStopUnavailable, CodeDeviceLocked, CodeDeviceNotAwake,
		CodeModeNotSelected, CodeRecovering, CodeSessionExists,
		CodeSessionNotFound, CodeSessionEnded, CodeSessionExpired,
		CodeReferenceNoSession, CodeOperationNotFound, CodeOperationUnknown,
		CodeObservationGone, CodeObservationExpired, CodeObservationClaimed,
		CodeObservationRetired, CodeNoReadyObservation, CodeNodeNotFound,
		CodeNodeDisabled, CodeDisplayUnavailable, CodeStopped,
		CodeRequestClosed, CodeCleanupUnconfirmed, CodeTextTargetUnknown,
		CodeTextResultUnknown, CodeStaleScreen, CodeTargetChanged,
		CodeForegroundChanged, CodeForegroundUnknown, CodeControlUI,
		CodeAppNotFound, CodeAppMoved, CodeAppTaskUnconfirmed, CodeTaskFocus,
		CodeContentUnavailable, CodeInspectionTooLarge, CodeRootUnavailable,
		CodeNotificationUnavailable:
		return http.StatusConflict
	case CodeDeviceUnavailable, CodeDisplayCreate, CodeHierarchyFailed,
		CodeTreeTooLarge, CodeAppLaunchFailed, CodeAppListUnavailable,
		CodeShellStartFailed, CodeShellOutput, CodeDeliveryUnavailable:
		return http.StatusServiceUnavailable
	case CodeActionUnknown, CodeAppLaunchUnknown, CodeShellUnknown:
		return http.StatusGatewayTimeout
	case CodeUnsupported:
		return http.StatusNotImplemented
	}
	return http.StatusInternalServerError
}

// Invalid builds a 422 parameter error without echoing input.
func Invalid(message string) *APIError {
	return NewError(http.StatusUnprocessableEntity, CodeInvalidRequest, message)
}

// WithDetail attaches one whitelisted fact to the error.
func (e *APIError) WithDetail(key string, value any) *APIError {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// WithNext sets the controlled recovery entry point.
func (e *APIError) WithNext(next string) *APIError {
	e.Next = next
	return e
}

// Errorf formats a controlled internal error.
func Errorf(code, message string) *APIError {
	return NewError(Status(code), code, message)
}

// Internal wraps an unexpected error as a stable internal error without
// exposing its text. Callers may log the original at a whitelisted boundary.
func Internal() *APIError {
	return NewError(http.StatusInternalServerError, CodeInternal, "服务内部错误")
}

// IsAPIError reports whether err is an *APIError.
func IsAPIError(err error) bool {
	var target *APIError
	return errors.As(err, &target)
}

// AsAPIError converts err to an *APIError, mapping unknown errors to internal.
func AsAPIError(err error) *APIError {
	var target *APIError
	if errors.As(err, &target) {
		return target
	}
	return Internal()
}
