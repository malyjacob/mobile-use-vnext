package android

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// longPressDurationMS is the swipe duration used to express a long press.
const longPressDurationMS = 700

// foregroundTimeout allows the two primary dumpsys reads.
func (d *Device) foregroundTimeout() time.Duration {
	if d.timeout < 10*time.Second {
		return 10 * time.Second
	}
	return d.timeout
}

// Foreground reads the target display foreground record.
//
// Virtual displays are read from the activity manager because their window
// focus is intentionally null. The primary display keeps its window-focus
// source; its task id comes from the activity dump only when both name the same
// component. A read failure of a source is unknown evidence, not a failure.
func (d *Device) Foreground(ctx context.Context, displayID int) (device.Foreground, error) {
	if d == nil {
		return device.Foreground{}, errNilDevice
	}
	if displayID < 0 {
		return device.Foreground{}, newErr(CodeInvalidArgs, "invalid display id", nil)
	}
	started := time.Now()
	timeout := d.foregroundTimeout()
	defer func() {
		d.logEvent(eventlog.Debug, "android.foreground.read",
			eventlog.Int("display_id", displayID),
			eventlog.Duration("ms", time.Since(started)))
	}()
	if displayID != 0 {
		activities, _, err := d.readCommand(ctx, "dumpsys activity activities", timeout)
		if err != nil {
			return unknownForeground("activities_unreadable"), nil
		}
		return parseActivityForeground(activities, displayID), nil
	}
	windowListing, haveWindow, err := d.readCommand(ctx, "dumpsys window displays", timeout)
	if err != nil {
		return unknownForeground("window_unreadable"), nil
	}
	activities, haveActivities, err := d.readCommand(ctx, "dumpsys activity activities", timeout)
	if err != nil {
		haveActivities = false
	}
	return foregroundFromSources(0, windowListing, activities, haveWindow, haveActivities), nil
}

// readCommand runs one read-only dumpsys command. ok is false when the command
// itself failed; the output is returned regardless so partial evidence can be
// inspected by the parser.
func (d *Device) readCommand(ctx context.Context, command string, timeout time.Duration) (string, bool, error) {
	result, err := d.shellWith(ctx, timeout, command)
	if err != nil {
		return "", false, err
	}
	return string(result.Stdout), result.ExitCode == 0, nil
}

// TaskList reads current application tasks (not historical recents entries).
func (d *Device) TaskList(ctx context.Context) ([]device.Task, error) {
	if d == nil {
		return nil, errNilDevice
	}
	started := time.Now()
	result, err := d.shellWith(ctx, d.timeout*2, "am stack list")
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, exitDetail(result.ExitCode)
	}
	tasks := parseTasks(string(result.Stdout))
	d.logEvent(eventlog.Debug, "android.tasks.read",
		eventlog.Int("count", len(tasks)),
		eventlog.Duration("ms", time.Since(started)))
	return tasks, nil
}

// Tap issues one display-bound tap. It never retries or changes display.
func (d *Device) Tap(ctx context.Context, displayID, x, y int) error {
	return d.dispatchInput(ctx, device.InputTap, displayID, x, y, x, y, "")
}

// LongPress issues one 700 ms swipe at the point.
func (d *Device) LongPress(ctx context.Context, displayID, x, y int) error {
	return d.dispatchInput(ctx, device.InputLongPress, displayID, x, y, x, y, "")
}

// Swipe issues one display-bound swipe gesture.
func (d *Device) Swipe(ctx context.Context, displayID, x1, y1, x2, y2 int) error {
	return d.dispatchInput(ctx, device.InputSwipe, displayID, x1, y1, x2, y2, "")
}

// swipeDurationMS is the plain swipe duration.
const swipeDurationMS = 300

// keyPattern is the accepted Android key-event token shape.
var keyPattern = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)

// Key issues one display-bound key event. The key is a KEYCODE name such as
// BACK or KEYCODE_BACK.
func (d *Device) Key(ctx context.Context, displayID int, key string) error {
	if d == nil {
		return errNilDevice
	}
	if displayID < 0 {
		return newErr(CodeInvalidArgs, "invalid display id", nil)
	}
	if !keyPattern.MatchString(key) {
		return newErr(CodeInvalidKey, "the key token is not a supported key name", nil)
	}
	return d.dispatchInput(ctx, device.InputKey, displayID, 0, 0, 0, 0, key)
}

func (d *Device) validatePoint(displayID, x, y int) error {
	if d == nil {
		return errNilDevice
	}
	if displayID < 0 {
		return newErr(CodeInvalidArgs, "invalid display id", nil)
	}
	if x < 0 || y < 0 {
		return newErr(CodeInvalidArgs, "input coordinates must not be negative", nil)
	}
	return nil
}

// dispatchInput validates one primitive, builds its exact argv and runs it.
func (d *Device) dispatchInput(ctx context.Context, kind device.InputKind, displayID, x, y, toX, toY int, key string) error {
	if kind != device.InputKey {
		if err := d.validatePoint(displayID, x, y); err != nil {
			return err
		}
		if kind == device.InputSwipe {
			if err := d.validatePoint(displayID, toX, toY); err != nil {
				return err
			}
		}
	}
	argv, err := inputArgv(kind, displayID, x, y, toX, toY, key)
	if err != nil {
		return err
	}
	return d.inputCommand(ctx, kind, displayID, argv...)
}

// inputCommand runs one already-validated input argv. Any non-zero status or
// transport failure is returned as an error and never retried.
func (d *Device) inputCommand(ctx context.Context, kind device.InputKind, displayID int, argv ...string) error {
	started := time.Now()
	result, err := d.Shell(ctx, strings.Join(argv, " "))
	if err != nil {
		d.logEvent(eventlog.Warn, "android.input.failed",
			eventlog.Str("kind", string(kind)),
			eventlog.Int("display_id", displayID),
			eventlog.Str("code", CodeOf(err)),
			eventlog.Duration("ms", time.Since(started)))
		return err
	}
	if result.ExitCode != 0 {
		d.logEvent(eventlog.Warn, "android.input.failed",
			eventlog.Str("kind", string(kind)),
			eventlog.Int("display_id", displayID),
			eventlog.Str("code", "exit_code"),
			eventlog.Duration("ms", time.Since(started)))
		return exitDetail(result.ExitCode)
	}
	d.logEvent(eventlog.Debug, "android.input.done",
		eventlog.Str("kind", string(kind)),
		eventlog.Int("display_id", displayID),
		eventlog.Duration("ms", time.Since(started)))
	return nil
}

// inputArgv builds the argv for one input primitive. It exists so the command
// construction is unit-testable without a device.
func inputArgv(kind device.InputKind, displayID, x, y, toX, toY int, key string) ([]string, error) {
	if displayID < 0 {
		return nil, newErr(CodeInvalidArgs, "invalid display id", nil)
	}
	prefix := []string{"input", "-d", strconv.Itoa(displayID)}
	switch kind {
	case device.InputTap:
		return append(prefix, "tap", strconv.Itoa(x), strconv.Itoa(y)), nil
	case device.InputLongPress:
		return append(prefix, "swipe",
			strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(x), strconv.Itoa(y),
			strconv.Itoa(longPressDurationMS)), nil
	case device.InputSwipe:
		return append(prefix, "swipe",
			strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(toX), strconv.Itoa(toY),
			strconv.Itoa(swipeDurationMS)), nil
	case device.InputKey:
		if !keyPattern.MatchString(key) {
			return nil, newErr(CodeInvalidKey, "the key token is not a supported key name", nil)
		}
		return append(prefix, "keyevent", key), nil
	}
	return nil, newErr(CodeInvalidArgs, "unsupported input kind", nil)
}
