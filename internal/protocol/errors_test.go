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
	if got := AccessRecovery(CodeAmbiguousPoint); got != nil {
		t.Fatalf("AccessRecovery(%s) = %+v, want no projection", CodeAmbiguousPoint, got)
	}
	if got := AccessRecovery(CodeContractMismatch); got == nil ||
		got.Entry != RecoveryEntryUpdate || got.Reference != RecoveryReferenceNone {
		t.Fatalf("AccessRecovery(%s) = %+v, want update/none", CodeContractMismatch, got)
	}
	if got := AccessRecovery(CodeInvalidRequest); got != nil {
		t.Fatalf("AccessRecovery(%s) = %+v, want no projection", CodeInvalidRequest, got)
	}
}
