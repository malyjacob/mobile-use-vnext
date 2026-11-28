package protocol

import (
	"errors"
	"net/http"
	"reflect"
)

// APIError is the controlled error shape shared across HTTP and later MCP.
// Code is stable; Message is a short controlled sentence. Details may only
// carry whitelisted controlled JSON facts: limit, budget, limit_ms, limit_s,
// app_code, field, task_ids, allowed_kinds, allowed_scopes,
// blocking_operation_id, start_operation_id, reason, target_actions,
// ancestor_ref, ancestor_actions, scope, scope_ref, candidates, blocking_refs,
// changed_ref, phase, kind, original_ref, recovery_attempted, stage, dispatch
// (focus/write/submit closed-state object), candidate_count (non-negative
// integer), target_center (objective center point []int), retry_parameter
// (controlled parameter name).
//
// The act static rejection taxonomy owns reason, target_actions, ancestor_ref,
// ancestor_actions, scope, scope_ref, candidates, and blocking_refs.
//
// The act revalidate rejection taxonomy owns phase, kind, reason, original_ref,
// recovery_attempted and, on the immediate response only, candidates and
// blocking_refs. Every act kind shares it on a 409 target_changed revalidation
// failure: phase is the single value revalidate, kind is the closed set
// tap/long_press/text/scroll, and reason is the closed nine-value revalidate
// enum (child_changed, target_missing, target_ambiguous, lineage_changed,
// capability_changed, scroll_scope_changed, trajectory_blocked, scope_blocked,
// point_changed). original_ref repeats the request's target.ref or
// container.ref and is always omitted for a coordinate-only request.
// recovery_attempted is []string and names the zero-dispatch recovery
// strategies that actually ran before the rejection (transient_retry,
// text_rebind, container_rebind); it is omitted when the first revalidation
// attempt rejected directly. changed_ref is the one optional public n-prefixed
// ancestor ref that attributes a target_changed rejection to the concrete
// changed ancestor; it is omitted whenever it cannot be located uniquely.
// candidates is fixed to []map[string]any whose objects contain ref, role,
// actions ([]string), bounds ([]int [left, top, right, bottom]), relation, and
// executable (bool); blocking_refs is []string. candidates and blocking_refs
// describe the freshly delivered observation of the immediate response and
// never enter the persisted operation error, so status(P) and a duplicate-P
// replay carry neither member.
//
// The text target and dispatch stage group owns stage/reason/dispatch/
// candidate_count and no other tool inherits that vocabulary: stage is
// target/focus/write/verify/submit and dispatch always carries the three keys
// focus, write and submit. It covers the static text_target_uncertain rejection
// and the text dispatch stages; it does not describe the phase=revalidate
// target_changed rejection, which follows the unified revalidate taxonomy above
// while the top-level text_input member states the untouched not_attempted
// write/submit pair. The static text_target_uncertain rejection (stage=target,
// reason=target_not_editable) may additionally carry the existing whitelisted
// candidates key when a legal candidate input field exists; the protected and
// disabled input exits carry no candidates.
//
// Details never carry user text, device output, trees, commands or credentials.
type APIError struct {
	Code       string
	Message    string
	Details    map[string]any
	HTTPStatus int
	Outcome    string // not_executed / partial / uncertain / "" (no active action)
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
	return &Error{Code: e.Code, Message: e.Message, Details: CloneErrorDetails(e.Details)}
}

// Error is the public controlled error object.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// CloneErrorDetails returns an independent copy of one error's controlled JSON
// details. It recursively copies string-keyed maps, slices and arrays while
// preserving concrete element types such as []map[string]any, []string and
// []int; scalars are copied by value. It deliberately avoids a JSON round trip,
// which would change every decoded number to float64. Non-JSON containers are
// outside the Details whitelist and are returned as-is.
func CloneErrorDetails(details map[string]any) map[string]any {
	if details == nil {
		return nil
	}
	return cloneErrorDetailValue(reflect.ValueOf(details)).Interface().(map[string]any)
}

// cloneErrorDetailValue copies only the container shapes that can occur in the
// controlled JSON Details whitelist. It keeps named slice/array/map types and
// their element types intact.
func cloneErrorDetailValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneErrorDetailValue(value.Elem())
		out := reflect.New(value.Type()).Elem()
		out.Set(cloned)
		return out
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return value
		}
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneErrorDetailValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(cloneErrorDetailValue(value.Index(index)))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for index := 0; index < value.Len(); index++ {
			out.Index(index).Set(cloneErrorDetailValue(value.Index(index)))
		}
		return out
	default:
		return value
	}
}

// NewError builds an APIError with the given status and outcome. It never
// derives a recovery projection: tool results are planned once by the engine
// and access-layer errors only by AccessRecovery.
func NewError(status int, code, message string) *APIError {
	return &APIError{Code: code, Message: message, HTTPStatus: status}
}

// NewErrorOutcome builds an APIError that also states the action outcome.
func NewErrorOutcome(status int, code, message, outcome string) *APIError {
	e := NewError(status, code, message)
	e.Outcome = outcome
	return e
}

// AccessRecovery returns the recovery projection of one HTTP access-layer
// error. It is the only recovery mapping outside the engine, and its scope is
// deliberately tiny: access admission can only produce a contract mismatch with
// a non-empty projection. Tool-level codes are planned by the engine; the
// engine never calls this function to build a tool result.
func AccessRecovery(code string) *Recovery {
	if code == CodeContractMismatch {
		return &Recovery{Entry: RecoveryEntryUpdate, Reference: RecoveryReferenceNone}
	}
	return nil
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
	CodeInvalidTarget      = "invalid_target"
	CodeNoSafePoint        = "no_safe_point"
	CodeNoSafeTrajectory   = "no_safe_trajectory"
	CodeInvalidCoordinates = "invalid_coordinates"
	CodeAmbiguousPoint     = "ambiguous_point"
	CodeInvalidKey         = "unsupported_key"
	CodeTextTooLong        = "text_too_long"
	CodeTextTargetUnknown  = "text_target_uncertain"
	CodeTextResultUnknown  = "text_result_uncertain"
	// CodeTextSubmitNotExecuted is the confirmed-partial fact of a composite
	// text action: the write is executed while the submission is certainly not
	// executed and no unknown physical side effect exists. It is 409 and always
	// accompanies outcome=partial; it never claims an uncertain receipt.
	CodeTextSubmitNotExecuted = "text_submit_not_executed"
	CodeTargetChanged         = "target_changed"
	CodeForegroundChanged     = "foreground_changed"
	CodeForegroundUnknown     = "foreground_unknown"
	CodeControlUI             = "control_ui_on_virtual_display"
	CodeActionUnknown         = "action_result_uncertain"
	CodeDisplayUnavailable    = "display_unavailable"
	CodeDisplayCreate         = "display_create_failed"
	CodeHierarchyFailed       = "hierarchy_unavailable"
	CodeTreeTooLarge          = "tree_too_large"
	CodeStaleScreen           = "stale_screen"
	CodeStopped               = "control_revoked"
	CodeRequestClosed         = "request_closed"
	CodeCleanupUnconfirmed    = "cleanup_unconfirmed"
	CodeUnsupported           = "unsupported_feature"
	CodeInternal              = "internal_error"
	CodeForbidden             = "forbidden"

	// Tool-surface codes. These describe controlled stages of the
	// discovery, app launch and managed shell tools; no new code changes the
	// meaning of the earlier codes above.
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

	// App lifecycle codes. They describe the unified app tool's controlled
	// stages and never change the meaning of an earlier code.
	CodeWindowActionPrimary = "window_action_not_supported_on_primary_display"
	CodeTaskNotFound        = "task_not_found"
	CodeTaskNotTracked      = "task_not_tracked"
	CodeTaskAmbiguous       = "task_target_ambiguous"
	CodeTaskRootShared      = "task_root_shared"
	CodeTaskMoveUnconfirmed = "task_move_unconfirmed"
	CodeCloseBorrowed       = "close_refused_borrowed"
	CodeKillNotOwned        = "kill_refused_not_owned"
	CodeKillTaskElsewhere   = "kill_refused_task_elsewhere"
	CodeKillForeground      = "kill_refused_foreground"
	CodeKillPlayback        = "kill_refused_playback"
	CodeKillProcessShared   = "kill_refused_process_shared"
	CodeKillUnconfirmed     = "kill_unconfirmed"
	CodeAppCapacity         = "app_capacity_exceeded"
	CodeTaskLifecycleBusy   = "task_lifecycle_busy"

	// Observation image stage codes. They describe the additional visual
	// material of one acquisition; they never replace a confirmed core tree or
	// the main action outcome.
	CodeImageCapture  = "image_capture_failed"
	CodeImageEncode   = "image_encode_failed"
	CodeImageTooLarge = "image_too_large"
	CodeImageStale    = "image_stale"
)

// Err is a sentinel used by the engine to signal that the work position is
// occupied. The caller maps it to a busy error with its own operation ID.
var ErrBusy = errors.New("device work position is occupied")

// Status returns the HTTP status for a code, defaulting to 500.
func Status(code string) int {
	switch code {
	case CodeInvalidRequest, CodeReferenceInvalid, CodeInvalidCoordinates,
		CodeAmbiguousPoint, CodeInvalidKey, CodeTextTooLong, CodeNodeUnsupported,
		CodeInvalidTarget, CodeNoSafePoint, CodeNoSafeTrajectory:
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
		CodeTextResultUnknown, CodeTextSubmitNotExecuted, CodeStaleScreen,
		CodeTargetChanged,
		CodeForegroundChanged, CodeForegroundUnknown, CodeControlUI,
		CodeAppNotFound, CodeAppMoved, CodeAppTaskUnconfirmed, CodeTaskFocus,
		CodeContentUnavailable, CodeInspectionTooLarge, CodeRootUnavailable,
		CodeNotificationUnavailable,
		CodeWindowActionPrimary, CodeTaskNotFound, CodeTaskNotTracked,
		CodeTaskAmbiguous, CodeTaskRootShared, CodeTaskMoveUnconfirmed,
		CodeCloseBorrowed, CodeKillNotOwned, CodeKillTaskElsewhere,
		CodeKillForeground, CodeKillPlayback, CodeKillProcessShared,
		CodeKillUnconfirmed, CodeAppCapacity, CodeTaskLifecycleBusy:
		return http.StatusConflict
	case CodeDeviceUnavailable, CodeDisplayCreate, CodeHierarchyFailed,
		CodeTreeTooLarge, CodeAppLaunchFailed, CodeAppListUnavailable,
		CodeShellStartFailed, CodeShellOutput, CodeDeliveryUnavailable,
		CodeImageCapture, CodeImageEncode, CodeImageTooLarge, CodeImageStale:
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
