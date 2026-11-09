package protocol

import (
	"net/http"
	"testing"
)

// TestAmbiguousPointStatus pins the wire mapping of the new static rejection.
// It is an admission error: nothing was claimed, captured or dispatched.
func TestAmbiguousPointStatus(t *testing.T) {
	if got := Status(CodeAmbiguousPoint); got != http.StatusUnprocessableEntity {
		t.Fatalf("Status(%s) = %d, want 422", CodeAmbiguousPoint, got)
	}
	if got := Status(CodeTargetChanged); got != http.StatusConflict {
		t.Fatalf("Status(%s) = %d, want 409", CodeTargetChanged, got)
	}
	if got := NextForCode(CodeAmbiguousPoint); got != "" {
		t.Fatalf("NextForCode(%s) = %q, want no forced route", CodeAmbiguousPoint, got)
	}
}
