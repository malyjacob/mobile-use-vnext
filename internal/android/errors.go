package android

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Error codes are stable, short tokens. They never contain device output, user
// text, commands or raw exception strings; callers map them to public errors.
const (
	CodeAvailability   = "device_unavailable"
	CodeTransport      = "adb_transport_failed"
	CodeCancelled      = "adb_cancelled"
	CodeTimeout        = "adb_timeout"
	CodeExitCode       = "adb_exit_code"
	CodeOutputTooBig   = "adb_output_oversize"
	CodeArtifact       = "u2_artifact_invalid"
	CodeServerStart    = "u2_server_start_failed"
	CodeServerStopped  = "u2_server_stopped"
	CodeRPC            = "u2_rpc_error"
	CodeRPCMethod      = "u2_rpc_unknown_method"
	CodeHierarchy      = "hierarchy_invalid"
	CodeDisplayAbsent  = "display_absent"
	CodeDisplayUnknown = "display_unconfirmed"
	CodeHelper         = "display_helper_failed"
	CodeInvalidKey     = "invalid_key"
	CodeInvalidArgs    = "invalid_argument"
)

// Error is the controlled failure shape returned by this package. The code is
// stable; the message is a fixed phrase and never embeds device output.
type Error struct {
	Code    string
	Message string
	Err     error
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Code + ": " + e.Message
}

// Unwrap exposes the cause for errors.Is/errors.As without exposing its text
// through Error().
func (e *Error) Unwrap() error { return e.Err }

func newErr(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Err: cause}
}

// CodeOf returns the stable code of a controlled error, or "" otherwise.
func CodeOf(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

var (
	errNilDevice    = newErr(CodeInvalidArgs, "device handle is not initialized", nil)
	errContextEnded = newErr(CodeCancelled, "the operation context ended", nil)
	errADBMissing   = newErr(CodeAvailability, "the adb executable is not available", nil)
)

// contextError converts a finished context into the controlled cancellation
// error so callers never receive a bare context.Canceled.
func contextError(ctx context.Context) error {
	if ctx == nil {
		return errContextEnded
	}
	if err := ctx.Err(); err != nil {
		return newErr(CodeCancelled, "the operation was cancelled", err)
	}
	return nil
}

// execFailure classifies one finished adb invocation. A non-zero exit is only
// converted to a transport error when adb itself failed; a device-side exit
// code is returned to the caller as evidence.
func execFailure(err error, timeout bool) *Error {
	if err == nil {
		return nil
	}
	if timeout {
		return newErr(CodeTimeout, "the adb command exceeded its deadline", err)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return newErr(CodeTransport, "the adb command failed", err)
	}
	return newErr(CodeAvailability, "the adb executable could not be started", err)
}

// exitDetail is a fixed phrase for one device-side non-zero status.
func exitDetail(code int) *Error {
	return newErr(CodeExitCode, fmt.Sprintf("the device command exited with status %d", code), nil)
}
