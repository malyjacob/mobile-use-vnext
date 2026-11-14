package protocol

import (
	"net/http"
	"testing"
)

// TestAPIErrorPublicOwnsNestedDetails is the ownership gate for the structured
// tap guidance. Public() must not expose mutable maps or controlled arrays
// shared with the stored operation error or with a second projection.
func TestAPIErrorPublicOwnsNestedDetails(t *testing.T) {
	t.Run("outer map", func(t *testing.T) {
		source := NewError(http.StatusUnprocessableEntity, CodeNodeUnsupported, "目标节点不支持点击").
			WithDetail("nearest_actionable_ref", "n516")
		first := source.Public()
		second := source.Public()

		first.Details["nearest_actionable_ref"] = "mutated"
		if got := source.Details["nearest_actionable_ref"]; got != "n516" {
			t.Fatalf("Public shared its outer details map with APIError: %#v", source.Details)
		}
		if got := second.Details["nearest_actionable_ref"]; got != "n516" {
			t.Fatalf("two Public projections shared their outer details map: %#v", second.Details)
		}
	})

	t.Run("nested map", func(t *testing.T) {
		source := NewError(http.StatusUnprocessableEntity, CodeNodeUnsupported, "目标节点不支持点击").
			WithDetail("safe_point", map[string]any{"x": 0, "y": 0})
		first := source.Public()
		second := source.Public()

		first.Details["safe_point"].(map[string]any)["x"] = 99
		if got := source.Details["safe_point"].(map[string]any)["x"]; got != 0 {
			t.Fatalf("Public shared a nested map with APIError: %#v", source.Details)
		}
		if got := second.Details["safe_point"].(map[string]any)["x"]; got != 0 {
			t.Fatalf("two Public projections shared a nested map: %#v", second.Details)
		}
	})

	t.Run("controlled slices", func(t *testing.T) {
		source := NewError(http.StatusConflict, CodeTaskAmbiguous, "任务不唯一").
			WithDetail("task_ids", []int{0, 42}).
			WithDetail("allowed_kinds", []string{"find", "list"}).
			WithDetail("nested_values", []any{map[string]any{"ok": true}}).
			WithDetail("nested_array", [1]map[string]any{{"ok": true}})
		first := source.Public()
		second := source.Public()

		first.Details["task_ids"].([]int)[0] = 99
		first.Details["allowed_kinds"].([]string)[0] = "mutated"
		first.Details["nested_values"].([]any)[0].(map[string]any)["ok"] = false
		nestedArray := first.Details["nested_array"].([1]map[string]any)
		nestedArray[0]["ok"] = false
		if got := source.Details["task_ids"].([]int)[0]; got != 0 {
			t.Fatalf("Public shared task_ids with APIError: %#v", source.Details)
		}
		if got := source.Details["allowed_kinds"].([]string)[0]; got != "find" {
			t.Fatalf("Public shared allowed_kinds with APIError: %#v", source.Details)
		}
		if got := source.Details["nested_values"].([]any)[0].(map[string]any)["ok"]; got != true {
			t.Fatalf("Public shared an array element map with APIError: %#v", source.Details)
		}
		if got := source.Details["nested_array"].([1]map[string]any)[0]["ok"]; got != true {
			t.Fatalf("Public shared a fixed-array element map with APIError: %#v", source.Details)
		}
		if got := second.Details["task_ids"].([]int)[0]; got != 0 {
			t.Fatalf("two Public projections shared a slice: %#v", second.Details)
		}
	})
}

// TestAPIErrorPublicPreservesControlledDetailTypes guards against replacing the
// controlled copy with a JSON round trip: []int and []string must retain their
// in-memory types while their backing arrays become independent.
func TestAPIErrorPublicPreservesControlledDetailTypes(t *testing.T) {
	source := NewError(http.StatusConflict, CodeTaskAmbiguous, "任务不唯一").
		WithDetail("task_ids", []int{0, 42}).
		WithDetail("allowed_kinds", []string{"find", "list"}).
		WithDetail("safe_point", map[string]any{"x": 0, "y": 9})
	public := source.Public()

	if _, ok := public.Details["task_ids"].([]int); !ok {
		t.Fatalf("task_ids type changed to %T", public.Details["task_ids"])
	}
	if _, ok := public.Details["allowed_kinds"].([]string); !ok {
		t.Fatalf("allowed_kinds type changed to %T", public.Details["allowed_kinds"])
	}
	if _, ok := public.Details["safe_point"].(map[string]any); !ok {
		t.Fatalf("safe_point type changed to %T", public.Details["safe_point"])
	}
}

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
