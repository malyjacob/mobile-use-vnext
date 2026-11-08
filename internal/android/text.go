package android

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// formatBounds renders Android's Rect.toShortString form `[l,t][r,b]`. The
// patched server matches this string exactly against the live node bounds.
func formatBounds(rect device.Rect) string {
	return fmt.Sprintf("[%d,%d][%d,%d]", rect.Left, rect.Top, rect.Right, rect.Bottom)
}

// textArguments builds the fixed argument order shared by get and set:
// (displayId, packageName, resourceId, className, description, bounds, width,
// height[, text]). The caller must pass the same identity to both calls.
func textArguments(target device.TextTarget, text *string) []any {
	arguments := []any{
		target.DisplayID,
		target.Package,
		target.ResourceID,
		target.ClassName,
		target.Description,
		formatBounds(target.Bounds),
		target.Width,
		target.Height,
	}
	if text != nil {
		arguments = append(arguments, *text)
	}
	return arguments
}

// textResult decodes one patched text RPC map. Missing keys stay zero so the
// caller can reject an incomplete answer instead of inventing values.
func textResult(value any) (device.TextResult, error) {
	mapping, ok := value.(map[string]any)
	if !ok {
		return device.TextResult{}, errRPCMalformed
	}
	var result device.TextResult
	if raw, present := mapping["candidateCount"]; present {
		count, ok := asInt(raw)
		if !ok {
			return device.TextResult{}, errRPCMalformed
		}
		result.CandidateCount = count
	}
	if raw, present := mapping["value"]; present && raw != nil {
		text, ok := raw.(string)
		if !ok {
			return device.TextResult{}, errRPCMalformed
		}
		result.Value = &text
	}
	if raw, present := mapping["issued"]; present {
		issued, ok := raw.(bool)
		if !ok {
			return device.TextResult{}, errRPCMalformed
		}
		result.Issued = issued
	}
	if raw, present := mapping["accepted"]; present {
		accepted, ok := raw.(bool)
		if !ok {
			return device.TextResult{}, errRPCMalformed
		}
		result.Accepted = accepted
	}
	return result, nil
}

// asInt accepts the JSON number forms a Java Map can produce.
func asInt(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(typed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

// validateTextTarget rejects a structurally incomplete identity before any
// device call, so a malformed request never reaches the accessibility tree.
func validateTextTarget(target device.TextTarget) error {
	if target.DisplayID < 0 {
		return newErr(CodeInvalidArgs, "invalid display id", nil)
	}
	if target.Package == "" {
		return newErr(CodeInvalidArgs, "the text target has no package", nil)
	}
	if target.Width <= 0 || target.Height <= 0 {
		return newErr(CodeInvalidArgs, "the text target has no display size", nil)
	}
	if target.Bounds.Empty() {
		return newErr(CodeInvalidArgs, "the text target has no bounds", nil)
	}
	return nil
}

// GetTextOnDisplay refreshes the uniquely matched node and returns its live
// accessibility text. It never writes to the device.
func (d *Device) GetTextOnDisplay(ctx context.Context, target device.TextTarget) (device.TextResult, error) {
	if d == nil {
		return device.TextResult{}, errNilDevice
	}
	if err := validateTextTarget(target); err != nil {
		return device.TextResult{}, err
	}
	started := time.Now()
	value, err := d.rpc(ctx, "getTextOnDisplay", textArguments(target, nil), d.timeout)
	if err != nil {
		d.logEvent(eventlog.Warn, "android.text.get_failed",
			eventlog.Int("display_id", target.DisplayID),
			eventlog.Str("code", CodeOf(err)),
			eventlog.Duration("ms", time.Since(started)))
		return device.TextResult{}, err
	}
	result, decodeErr := textResult(value)
	if decodeErr != nil {
		return device.TextResult{}, decodeErr
	}
	d.logEvent(eventlog.Debug, "android.text.get_done",
		eventlog.Int("display_id", target.DisplayID),
		eventlog.Int("count", result.CandidateCount),
		eventlog.Duration("ms", time.Since(started)))
	return result, nil
}

// SetTextOnDisplay issues one display-bound text replacement.
//
// The call is never replayed: once the request is sent, a transport failure is
// reported as uncertain by the caller because the device may already have run
// it. Argument order matches the fixed server patch exactly.
func (d *Device) SetTextOnDisplay(ctx context.Context, target device.TextTarget, text string) (device.TextResult, error) {
	if d == nil {
		return device.TextResult{}, errNilDevice
	}
	if err := validateTextTarget(target); err != nil {
		return device.TextResult{}, err
	}
	started := time.Now()
	value, err := d.rpc(ctx, "setTextOnDisplay", textArguments(target, &text), d.timeout)
	if err != nil {
		d.logEvent(eventlog.Warn, "android.text.set_failed",
			eventlog.Int("display_id", target.DisplayID),
			eventlog.Str("code", CodeOf(err)),
			eventlog.Duration("ms", time.Since(started)))
		return device.TextResult{}, err
	}
	result, decodeErr := textResult(value)
	if decodeErr != nil {
		return device.TextResult{}, decodeErr
	}
	d.logEvent(eventlog.Debug, "android.text.set_done",
		eventlog.Int("display_id", target.DisplayID),
		eventlog.Int("count", result.CandidateCount),
		eventlog.Bool("issued", result.Issued),
		eventlog.Bool("accepted", result.Accepted),
		eventlog.Duration("ms", time.Since(started)))
	return result, nil
}
