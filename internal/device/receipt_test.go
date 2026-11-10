package device

import "testing"

func TestDispatchReceiptZeroIsUnknown(t *testing.T) {
	var receipt DispatchReceipt
	if receipt != DispatchUnknown {
		t.Fatalf("zero receipt=%d, want DispatchUnknown", receipt)
	}
	if DispatchNotDispatched == DispatchUnknown || DispatchTerminated == DispatchUnknown {
		t.Fatal("receipt constants must be distinct")
	}
}
