package protocol

import (
	"encoding/json"
	"net/http"
	"strings"
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

// TestStaticTargetTaxonomyStatus pins the tap/long_press ref static error
// classification: all three codes are 422 admission rejections, while the
// unchanged node-level and hierarchy codes keep their statuses.
func TestStaticTargetTaxonomyStatus(t *testing.T) {
	for _, code := range []string{CodeInvalidTarget, CodeNoSafePoint, CodeNodeUnsupported} {
		if got := Status(code); got != http.StatusUnprocessableEntity {
			t.Fatalf("Status(%s) = %d, want 422", code, got)
		}
		if got := AccessRecovery(code); got != nil {
			t.Fatalf("AccessRecovery(%s) = %+v, want no projection", code, got)
		}
	}
	if got := Status(CodeNodeNotFound); got != http.StatusConflict {
		t.Fatalf("Status(%s) = %d, want 409", CodeNodeNotFound, got)
	}
	if got := Status(CodeNodeDisabled); got != http.StatusConflict {
		t.Fatalf("Status(%s) = %d, want 409", CodeNodeDisabled, got)
	}
	if got := Status(CodeHierarchyFailed); got != http.StatusServiceUnavailable {
		t.Fatalf("Status(%s) = %d, want 503", CodeHierarchyFailed, got)
	}
}

// TestAPIErrorPublicCopiesStaticTaxonomyDetails proves the new list and point
// facts follow the same ownership rule as the baseline guidance details.
func TestAPIErrorPublicCopiesStaticTaxonomyDetails(t *testing.T) {
	source := NewError(http.StatusUnprocessableEntity, CodeNoSafePoint, "无安全落点").
		WithDetail("reason", "mixed").
		WithDetail("scope", "target").
		WithDetail("scope_ref", "n2").
		WithDetail("target_actions", []string{}).
		WithDetail("occluding_refs", []string{"n3", "n4"}).
		WithDetail("ambiguous_refs", []string{"n5", "n6"}).
		WithDetail("owner_refs", []string{"n7"}).
		WithDetail("suggested_point", map[string]any{"x": 0, "y": 0})
	first := source.Public()
	second := source.Public()

	first.Details["occluding_refs"].([]string)[0] = "mutated"
	first.Details["target_actions"] = []string{"tap"}
	first.Details["suggested_point"].(map[string]any)["x"] = 99
	if got := source.Details["occluding_refs"].([]string)[0]; got != "n3" {
		t.Fatalf("Public shared occluding_refs with APIError: %#v", source.Details)
	}
	if got := source.Details["target_actions"].([]string); len(got) != 0 {
		t.Fatalf("Public shared target_actions with APIError: %#v", source.Details)
	}
	if got := source.Details["suggested_point"].(map[string]any)["x"]; got != 0 {
		t.Fatalf("Public shared suggested_point with APIError: %#v", source.Details)
	}
	if got := second.Details["suggested_point"].(map[string]any)["x"]; got != 0 {
		t.Fatalf("two Public projections shared suggested_point: %#v", second.Details)
	}
	if actions, ok := second.Details["target_actions"].([]string); !ok || actions == nil || len(actions) != 0 {
		t.Fatalf("empty target_actions lost its non-nil empty array: %#v", second.Details["target_actions"])
	}
}

// TestErrorEncodesEmptyActionList proves the declared action array keeps `[]`
// on the wire instead of becoming null or disappearing.
func TestErrorEncodesEmptyActionList(t *testing.T) {
	encoded, err := json.Marshal(&Error{
		Code:    CodeNodeUnsupported,
		Message: "目标节点不含点击、长按或文本输入能力",
		Details: map[string]any{
			"reason":         "target_missing_action",
			"target_actions": []string{},
		},
	})
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"target_actions":[]`) {
		t.Fatalf("empty target_actions encoded as %s, want []", text)
	}
}
