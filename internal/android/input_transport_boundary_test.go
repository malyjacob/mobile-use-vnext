package android

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

func TestSwipeRejectsNegativeEndpointBeforeTransport(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "calls")
	d := newStubDevice(t, "input")
	d.testEnv = append(d.testEnv, "MOBILE_USE_FAKE_ADB_CALLS="+calls)
	d.setShellV2(true)

	cases := []struct {
		name   string
		x1, y1 int
		x2, y2 int
	}{
		{name: "negative destination x", x1: 10, y1: 20, x2: -1, y2: 40},
		{name: "negative destination y", x1: 10, y1: 20, x2: 30, y2: -1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			receipt, err := d.Swipe(t.Context(), 61, testCase.x1, testCase.y1,
				testCase.x2, testCase.y2, 300)
			if CodeOf(err) != CodeInvalidArgs {
				t.Fatalf("code = %q error = %v", CodeOf(err), err)
			}
			if receipt != device.DispatchNotDispatched {
				t.Fatalf("receipt = %d, want not dispatched", receipt)
			}
		})
	}

	raw, err := os.ReadFile(calls)
	if err == nil && len(raw) != 0 {
		t.Fatalf("negative endpoint reached transport %d time(s)", len(raw))
	}
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read transport call count: %v", err)
	}
}
