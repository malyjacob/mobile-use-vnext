package android

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

func TestExecutionIdentityAcceptsCommaAndNewlineFeatures(t *testing.T) {
	for _, transcript := range []string{"identity-comma", "identity-lines"} {
		t.Run(transcript, func(t *testing.T) {
			d := newStubDevice(t, transcript)
			identity, err := d.ExecutionIdentity(t.Context())
			if err != nil {
				t.Fatalf("ExecutionIdentity: %v", err)
			}
			if identity.DeviceID != "device-test" || identity.BootID != testBootID {
				t.Fatalf("identity=%+v", identity)
			}
		})
	}
}

func TestExecutionIdentityRejectsMissingFeatureAndMalformedValues(t *testing.T) {
	cases := []string{
		"features-missing",
		"features-partial",
		"identity-bad-device",
		"identity-bad-boot",
	}
	for _, transcript := range cases {
		t.Run(transcript, func(t *testing.T) {
			d := newStubDevice(t, transcript)
			if _, err := d.ExecutionIdentity(t.Context()); CodeOf(err) != CodeAvailability {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func TestInputReceiptSeparatesRemoteExitFromTransport(t *testing.T) {
	t.Run("remote exit", func(t *testing.T) {
		d := newStubDevice(t, "input-exit")
		d.setShellV2(true)
		receipt, err := d.Tap(t.Context(), 61, 10, 20)
		if receipt != device.DispatchTerminated || CodeOf(err) != CodeExitCode {
			t.Fatalf("receipt=%d code=%q err=%v", receipt, CodeOf(err), err)
		}
		if !d.shellV2Confirmed() {
			t.Fatal("a terminated remote exit must keep shell v2 confirmed")
		}
	})

	t.Run("transport", func(t *testing.T) {
		d := newStubDevice(t, "input-transport")
		d.setShellV2(true)
		receipt, err := d.Tap(t.Context(), 61, 10, 20)
		if receipt != device.DispatchUnknown || CodeOf(err) != CodeTransport {
			t.Fatalf("receipt=%d code=%q err=%v", receipt, CodeOf(err), err)
		}
		if d.shellV2Confirmed() {
			t.Fatal("a transport failure must require identity revalidation")
		}
	})
}

func TestInputValidationIsNotDispatchedAndNotRetried(t *testing.T) {
	d := newStubDevice(t, "input")
	receipt, err := d.Tap(t.Context(), -1, 10, 20)
	if receipt != device.DispatchNotDispatched || err == nil {
		t.Fatalf("receipt=%d err=%v", receipt, err)
	}

	calls := filepath.Join(t.TempDir(), "calls")
	d.testEnv = append(d.testEnv, "MOBILE_USE_FAKE_ADB_CALLS="+calls)
	d.setShellV2(true)
	receipt, err = d.Tap(t.Context(), 61, 10, 20)
	if receipt != device.DispatchTerminated || err != nil {
		t.Fatalf("receipt=%d err=%v", receipt, err)
	}
	raw, readErr := os.ReadFile(calls)
	if readErr != nil {
		t.Fatalf("read call count: %v", readErr)
	}
	if got, convErr := strconv.Atoi(string(raw)); convErr != nil || got != 1 {
		t.Fatalf("calls=%q err=%v, want one dispatch", raw, convErr)
	}
}

func TestInputCancellationIsUnknownAndNotRetried(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	d := newStubDevice(t, "hang")
	d.testEnv = append(d.testEnv, "MOBILE_USE_FAKE_ADB_CALLS="+calls)
	d.setShellV2(true)
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	receipt, err := d.Tap(ctx, 61, 10, 20)
	if receipt != device.DispatchUnknown || CodeOf(err) != CodeCancelled {
		t.Fatalf("receipt=%d code=%q err=%v", receipt, CodeOf(err), err)
	}
	if d.shellV2Confirmed() {
		t.Fatal("a cancelled dispatch must require identity revalidation")
	}
	raw, readErr := os.ReadFile(calls)
	if readErr != nil {
		t.Fatalf("read call count: %v", readErr)
	}
	if got, convErr := strconv.Atoi(string(raw)); convErr != nil || got != 1 {
		t.Fatalf("calls=%q err=%v, want one dispatch", raw, convErr)
	}
}

func TestInputRejectsUnverifiedShellV2BeforeDispatch(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	d := newStubDevice(t, "input")
	d.testEnv = append(d.testEnv, "MOBILE_USE_FAKE_ADB_CALLS="+calls)
	receipt, err := d.Tap(context.Background(), 61, 10, 20)
	if receipt != device.DispatchNotDispatched || CodeOf(err) != CodeAvailability {
		t.Fatalf("receipt=%d code=%q err=%v", receipt, CodeOf(err), err)
	}
	if raw, readErr := os.ReadFile(calls); readErr == nil && len(raw) != 0 {
		t.Fatalf("unverified shell dispatched %d commands", len(raw))
	}
}
