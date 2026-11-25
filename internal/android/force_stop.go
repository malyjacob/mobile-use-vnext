package android

import (
	"context"
	"strconv"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// Package-level force-stop primitive.
//
// This is one dispatch of `am force-stop --user <id> <pkg>` for one exact
// package and one Android user. The engine owns every decision around it: which
// package may be stopped, whether the session still owns it, which guards apply
// and what the outcome is. The adapter reports only the command boundary and the
// device exit status that boundary proves.
//
// One call is exactly one dispatch. A force-stop is never retried: the engine
// maps a completed non-zero exit or an unknown receipt to an uncertain outcome
// instead of sending the command twice. No package name, command, user id or
// device output is ever logged; the event carries a state token and a duration.

// ForceStopPackage sends one force-stop for one package under one Android user.
//
// The receipt is the dispatch fact: not_dispatched means the command provably
// never left the host, terminated means the device command completed, and unknown
// means it may have reached the device. ExitCode is only set for a terminated
// command, because only then does the status belong to the device command; a
// non-zero status is returned as a completed fact, not an error, so the caller can
// report an unconfirmed stop rather than a failed call.
func (d *Device) ForceStopPackage(ctx context.Context, pkg string, androidUserID int) (
	device.ForceStopResult, error,
) {
	result := device.ForceStopResult{Receipt: device.DispatchNotDispatched}
	if d == nil {
		return result, errNilDevice
	}
	if !validPackageName(pkg) {
		return result, newErr(CodeInvalidArgs, "invalid package name", nil)
	}
	if androidUserID < 0 {
		return result, newErr(CodeInvalidArgs, "invalid Android user id", nil)
	}
	if err := contextError(ctx); err != nil {
		return result, err
	}
	started := time.Now()
	command := "am force-stop --user " + strconv.Itoa(androidUserID) + " " + shellQuote(pkg)
	shell, receipt, err := d.runShellV2(ctx, d.processTimeout(), command)
	result.Receipt = receipt
	if err != nil {
		d.logForceStop(receipt, started)
		return result, err
	}
	if receipt == device.DispatchTerminated {
		result.ExitCode = shell.ExitCode
		result.HasExitCode = true
	}
	d.logForceStop(receipt, started)
	return result, nil
}

// logForceStop records the one command boundary with whitelisted fields only.
func (d *Device) logForceStop(receipt device.DispatchReceipt, started time.Time) {
	d.logEvent(eventlog.Info, "android.package.force_stop",
		eventlog.Str("state", receiptName(receipt)),
		eventlog.Duration("ms", time.Since(started)))
}
