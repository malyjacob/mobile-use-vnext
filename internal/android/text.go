package android

import (
	"context"
	"fmt"
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

// errTextResultInvalid classifies a complete-looking text RPC value whose
// members or combination cannot come from the fixed helper. The code stays the
// stable RPC code while the device sentinel lets the engine tell a malformed
// result apart from a transport failure without importing this package.
var errTextResultInvalid = newErr(CodeRPC, "the text result is invalid", device.ErrTextResultInvalid)

// textResult decodes one patched get RPC map. Both candidateCount and value
// must be present with usable types: the ENTER gate compares the refreshed
// value of the same node, so a missing member is an invalid result, never an
// empty string or a zero count. Optional issued/accepted members are validated
// when present so a type-mixed response is not silently accepted.
func textResult(value any) (device.TextResult, error) {
	mapping, ok := value.(map[string]any)
	if !ok {
		return device.TextResult{}, errTextResultInvalid
	}
	var result device.TextResult
	rawCount, present := mapping["candidateCount"]
	if !present {
		return device.TextResult{}, errTextResultInvalid
	}
	count, ok := strictInt(rawCount)
	if !ok || count < 0 {
		return device.TextResult{}, errTextResultInvalid
	}
	result.CandidateCount = count
	rawValue, present := mapping["value"]
	if !present {
		return device.TextResult{}, errTextResultInvalid
	}
	if rawValue != nil {
		text, ok := rawValue.(string)
		if !ok {
			return device.TextResult{}, errTextResultInvalid
		}
		result.Value = &text
	}
	for _, name := range []string{"issued", "accepted"} {
		if raw, present := mapping[name]; present {
			if _, ok := raw.(bool); !ok {
				return device.TextResult{}, errTextResultInvalid
			}
		}
	}
	return result, nil
}

// strictInt accepts only JSON numeric integer forms. The patched helpers emit
// JSON numbers, and a string or boolean must not become a count.
func strictInt(value any) (int, bool) {
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
	}
	return 0, false
}

// textSetResult requires every member documented by the pinned set RPC and
// rejects combinations the fixed helper cannot produce. A complete method
// result proves that this call is no longer in flight even if its semantic
// issued/accepted values are false; an incomplete or contradictory value is an
// unknown receipt and is never converted to zero/false/"".
//
// The fixed helper has exactly two normal shapes: non-unique candidates never
// call the write primitive (issued=false, accepted=false, value=null), and a
// unique candidate calls it once (issued=true, with an optional refreshed
// value). Unique-but-not-issued and non-unique-but-issued are contradictions.
func textSetResult(value any) (device.TextResult, error) {
	mapping, ok := value.(map[string]any)
	if !ok {
		return device.TextResult{}, errTextResultInvalid
	}
	var result device.TextResult

	rawCount, present := mapping["candidateCount"]
	if !present {
		return device.TextResult{}, errTextResultInvalid
	}
	count, ok := strictInt(rawCount)
	if !ok || count < 0 {
		return device.TextResult{}, errTextResultInvalid
	}
	result.CandidateCount = count

	rawValue, present := mapping["value"]
	if !present {
		return device.TextResult{}, errTextResultInvalid
	}
	if rawValue != nil {
		text, ok := rawValue.(string)
		if !ok {
			return device.TextResult{}, errTextResultInvalid
		}
		result.Value = &text
	}

	rawIssued, present := mapping["issued"]
	if !present {
		return device.TextResult{}, errTextResultInvalid
	}
	issued, ok := rawIssued.(bool)
	if !ok {
		return device.TextResult{}, errTextResultInvalid
	}
	result.Issued = issued

	rawAccepted, present := mapping["accepted"]
	if !present {
		return device.TextResult{}, errTextResultInvalid
	}
	accepted, ok := rawAccepted.(bool)
	if !ok {
		return device.TextResult{}, errTextResultInvalid
	}
	result.Accepted = accepted

	if count == 1 {
		if !issued {
			return device.TextResult{}, errTextResultInvalid
		}
		return result, nil
	}
	if issued || accepted || result.Value != nil {
		return device.TextResult{}, errTextResultInvalid
	}
	return result, nil
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
		return device.TextResult{Receipt: device.DispatchNotDispatched}, errNilDevice
	}
	if err := validateTextTarget(target); err != nil {
		return device.TextResult{Receipt: device.DispatchNotDispatched}, err
	}
	started := time.Now()
	value, err := d.rpc(ctx, "setTextOnDisplay", textArguments(target, &text), d.timeout)
	if err != nil {
		d.logEvent(eventlog.Warn, "android.text.set_failed",
			eventlog.Int("display_id", target.DisplayID),
			eventlog.Str("code", CodeOf(err)),
			eventlog.Duration("ms", time.Since(started)))
		receipt := device.DispatchUnknown
		if CodeOf(err) == CodeInvalidArgs {
			receipt = device.DispatchNotDispatched
		}
		return device.TextResult{Receipt: receipt}, err
	}
	result, decodeErr := textSetResult(value)
	if decodeErr != nil {
		return device.TextResult{Receipt: device.DispatchUnknown}, decodeErr
	}
	result.Receipt = device.DispatchTerminated
	d.logEvent(eventlog.Debug, "android.text.set_done",
		eventlog.Int("display_id", target.DisplayID),
		eventlog.Int("count", result.CandidateCount),
		eventlog.Bool("issued", result.Issued),
		eventlog.Bool("accepted", result.Accepted),
		eventlog.Duration("ms", time.Since(started)))
	return result, nil
}
