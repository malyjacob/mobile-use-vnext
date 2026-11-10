package android

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

const (
	shellV2Feature    = "shell_v2"
	maxDeviceIDLength = 128
)

var bootIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

func (d *Device) setShellV2(confirmed bool) {
	if d == nil {
		return
	}
	d.execMu.Lock()
	d.shellV2 = confirmed
	d.execMu.Unlock()
}

func (d *Device) shellV2Confirmed() bool {
	if d == nil {
		return false
	}
	d.execMu.RLock()
	confirmed := d.shellV2
	d.execMu.RUnlock()
	return confirmed
}

// ExecutionIdentity verifies the input transport and returns the private
// identity needed to recognize a later Android boot. Raw values are returned
// only as device facts and are never included in errors or logs.
func (d *Device) ExecutionIdentity(ctx context.Context) (device.BootIdentity, error) {
	if d == nil {
		return device.BootIdentity{}, errNilDevice
	}
	if err := d.verifyShellV2(ctx); err != nil {
		return device.BootIdentity{}, err
	}

	deviceID, err := d.readExecutionToken(ctx, "getprop ro.serialno")
	if err != nil {
		return device.BootIdentity{}, err
	}
	bootID, err := d.readExecutionToken(ctx, "cat /proc/sys/kernel/random/boot_id")
	if err != nil {
		return device.BootIdentity{}, err
	}
	if !bootIDPattern.MatchString(bootID) {
		d.setShellV2(false)
		return device.BootIdentity{}, newErr(CodeAvailability,
			"the Android boot identity is unreadable", nil)
	}
	return device.BootIdentity{
		DeviceID: deviceID,
		BootID:   strings.ToLower(bootID),
	}, nil
}

// verifyShellV2 accepts only a complete feature token. adb versions differ in
// whether features are comma-separated or emitted one per line.
func (d *Device) verifyShellV2(ctx context.Context) error {
	exe, err := d.exePath()
	if err != nil {
		d.setShellV2(false)
		return err
	}
	result, err := d.run(ctx, d.timeout, exe, d.adbArgs("features")...)
	if err != nil {
		d.setShellV2(false)
		return err
	}
	if result.ExitCode != 0 {
		d.setShellV2(false)
		return newErr(CodeAvailability, "the adb shell capability is unreadable", nil)
	}
	for _, token := range strings.FieldsFunc(string(result.Stdout), func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	}) {
		if token == shellV2Feature {
			d.setShellV2(true)
			return nil
		}
	}
	d.setShellV2(false)
	return newErr(CodeAvailability, "the device does not negotiate shell v2", nil)
}

func (d *Device) readExecutionToken(ctx context.Context, command string) (string, error) {
	result, receipt, err := d.runShellV2(ctx, d.timeout, command)
	if err != nil {
		return "", err
	}
	if receipt != device.DispatchTerminated || result.ExitCode != 0 {
		d.setShellV2(false)
		return "", newErr(CodeAvailability, "the device identity is unreadable", nil)
	}
	token := strings.TrimSpace(string(result.Stdout))
	if token == "" || len(token) > maxDeviceIDLength ||
		strings.IndexFunc(token, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		}) >= 0 {
		d.setShellV2(false)
		return "", newErr(CodeAvailability, "the device identity is malformed", nil)
	}
	return token, nil
}

// runShellV2 executes one command after ExecutionIdentity has proved that the
// transport forwards remote exits. A completed call is terminated even when its
// device exit status is non-zero; transport, cancellation and local failures
// remain unknown unless they are provably pre-dispatch.
func (d *Device) runShellV2(ctx context.Context, timeout time.Duration, command string) (
	ShellResult, device.DispatchReceipt, error,
) {
	if d == nil {
		return ShellResult{}, device.DispatchNotDispatched, errNilDevice
	}
	if command == "" {
		return ShellResult{}, device.DispatchNotDispatched,
			newErr(CodeInvalidArgs, "empty shell command", nil)
	}
	if err := contextError(ctx); err != nil {
		return ShellResult{}, device.DispatchNotDispatched, err
	}
	if !d.shellV2Confirmed() {
		return ShellResult{}, device.DispatchNotDispatched,
			newErr(CodeAvailability, "the input transport has not negotiated shell v2", nil)
	}
	exe, err := d.exePath()
	if err != nil {
		return ShellResult{}, device.DispatchNotDispatched, err
	}
	result, started, err := d.runInputModeStarted(ctx, timeout, nil, true,
		exe, d.adbArgs("shell", command)...)
	if err != nil {
		if !started {
			return result, device.DispatchNotDispatched, err
		}
		d.setShellV2(false)
		return result, device.DispatchUnknown, err
	}
	return result, device.DispatchTerminated, nil
}
