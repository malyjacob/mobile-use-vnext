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
			WithDetail("reason", "blocked")
		first := source.Public()
		second := source.Public()

		first.Details["reason"] = "mutated"
		if got := source.Details["reason"]; got != "blocked" {
			t.Fatalf("Public shared its outer details map with APIError: %#v", source.Details)
		}
		if got := second.Details["reason"]; got != "blocked" {
			t.Fatalf("two Public projections shared their outer details map: %#v", second.Details)
		}
	})

	t.Run("nested map", func(t *testing.T) {
		source := NewError(http.StatusUnprocessableEntity, CodeNodeUnsupported, "目标节点不支持点击").
			WithDetail("candidates", []map[string]any{{"ref": "n2", "role": "button"}})
		first := source.Public()
		second := source.Public()

		first.Details["candidates"].([]map[string]any)[0]["ref"] = "mutated"
		if got := source.Details["candidates"].([]map[string]any)[0]["ref"]; got != "n2" {
			t.Fatalf("Public shared a nested map with APIError: %#v", source.Details)
		}
		if got := second.Details["candidates"].([]map[string]any)[0]["ref"]; got != "n2" {
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
		WithDetail("candidates", []map[string]any{{
			"ref":        "n2",
			"role":       "button",
			"actions":    []string{"tap"},
			"bounds":     []int{0, 10, 20, 30},
			"relation":   "overlapping",
			"executable": true,
		}}).
		WithDetail("blocking_refs", []string{"n3"})
	public := source.Public()

	if _, ok := public.Details["task_ids"].([]int); !ok {
		t.Fatalf("task_ids type changed to %T", public.Details["task_ids"])
	}
	if _, ok := public.Details["allowed_kinds"].([]string); !ok {
		t.Fatalf("allowed_kinds type changed to %T", public.Details["allowed_kinds"])
	}
	candidates, ok := public.Details["candidates"].([]map[string]any)
	if !ok || len(candidates) != 1 {
		t.Fatalf("candidates type changed to %T", public.Details["candidates"])
	}
	if _, ok := candidates[0]["actions"].([]string); !ok {
		t.Fatalf("candidates[0].actions type changed to %T", candidates[0]["actions"])
	}
	if _, ok := candidates[0]["bounds"].([]int); !ok {
		t.Fatalf("candidates[0].bounds type changed to %T", candidates[0]["bounds"])
	}
	if _, ok := public.Details["blocking_refs"].([]string); !ok {
		t.Fatalf("blocking_refs type changed to %T", public.Details["blocking_refs"])
	}
}

// TestNoSafeTrajectoryStatus pins the added static scroll rejection as an
// admission error: nothing was claimed, captured or dispatched.
func TestNoSafeTrajectoryStatus(t *testing.T) {
	if got := Status(CodeNoSafeTrajectory); got != http.StatusUnprocessableEntity {
		t.Fatalf("Status(%s) = %d, want 422", CodeNoSafeTrajectory, got)
	}
	if got := AccessRecovery(CodeNoSafeTrajectory); got != nil {
		t.Fatalf("AccessRecovery(%s) = %+v, want no projection", CodeNoSafeTrajectory, got)
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

// TestStaticTargetTaxonomyStatus pins the act static error classification:
// all four codes are 422 admission rejections, while the unchanged node-level
// and hierarchy codes keep their statuses.
func TestStaticTargetTaxonomyStatus(t *testing.T) {
	for _, code := range []string{
		CodeInvalidTarget, CodeNoSafePoint, CodeNoSafeTrajectory, CodeNodeUnsupported,
	} {
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

// TestCloneErrorDetailsCopiesStaticCandidates proves candidates and
// blocking_refs are deep-copied with their concrete in-memory container types.
// Mutating the source maps, outer slices, inner action slices or bounds slices
// must not reach the clone.
func TestCloneErrorDetailsCopiesStaticCandidates(t *testing.T) {
	candidates := []map[string]any{
		{
			"ref":        "n5232",
			"role":       "button",
			"actions":    []string{"tap"},
			"bounds":     []int{915, 1675, 1028, 1787},
			"relation":   "overlapping",
			"executable": true,
		},
		{
			"ref":        "n5231",
			"role":       "group",
			"actions":    []string{},
			"bounds":     []int{0, 1600, 1080, 1900},
			"relation":   "child",
			"executable": false,
		},
	}
	blockingRefs := []string{"n5230", "n5253"}
	source := map[string]any{
		"reason":        "colocated_ambiguous",
		"scope":         "target",
		"scope_ref":     "n5233",
		"candidates":    candidates,
		"blocking_refs": blockingRefs,
	}

	cloned := CloneErrorDetails(source)
	clonedCandidates, ok := cloned["candidates"].([]map[string]any)
	if !ok || len(clonedCandidates) != len(candidates) {
		t.Fatalf("cloned candidates = %T/%d, want []map[string]any/%d",
			cloned["candidates"], len(clonedCandidates), len(candidates))
	}

	candidates[0]["ref"] = "mutated"
	candidates[0]["actions"].([]string)[0] = "long_press"
	candidates[0]["bounds"].([]int)[0] = 1
	candidates[0]["executable"] = false
	blockingRefs[0] = "mutated"

	if got := clonedCandidates[0]["ref"]; got != "n5232" {
		t.Fatalf("candidate ref followed the source mutation: %#v", got)
	}
	if got := clonedCandidates[0]["actions"].([]string)[0]; got != "tap" {
		t.Fatalf("candidate actions followed the source mutation: %#v", got)
	}
	if got := clonedCandidates[0]["bounds"].([]int)[0]; got != 915 {
		t.Fatalf("candidate bounds followed the source mutation: %#v", got)
	}
	if got, ok := clonedCandidates[0]["executable"].(bool); !ok || !got {
		t.Fatalf("candidate executable followed the source mutation: %#v", clonedCandidates[0]["executable"])
	}
	if actions, ok := clonedCandidates[1]["actions"].([]string); !ok || actions == nil || len(actions) != 0 {
		t.Fatalf("empty candidate actions lost its non-nil []string: %#v", clonedCandidates[1]["actions"])
	}
	if got := cloned["blocking_refs"].([]string)[0]; got != "n5230" {
		t.Fatalf("blocking_refs followed the source mutation: %#v", got)
	}
}

// TestAPIErrorPublicCopiesStaticTaxonomyDetails proves the unified list,
// candidate and blocking facts follow the same ownership rule as every other
// controlled detail.
func TestAPIErrorPublicCopiesStaticTaxonomyDetails(t *testing.T) {
	source := NewError(http.StatusUnprocessableEntity, CodeNoSafePoint, "无安全落点").
		WithDetail("reason", "mixed").
		WithDetail("scope", "target").
		WithDetail("scope_ref", "n2").
		WithDetail("target_actions", []string{}).
		WithDetail("candidates", []map[string]any{{
			"ref":        "n3",
			"role":       "button",
			"actions":    []string{"tap"},
			"bounds":     []int{0, 10, 20, 30},
			"relation":   "overlapping",
			"executable": true,
		}}).
		WithDetail("blocking_refs", []string{"n4", "n5"})
	first := source.Public()
	second := source.Public()

	firstCandidate := first.Details["candidates"].([]map[string]any)[0]
	firstCandidate["ref"] = "mutated"
	firstCandidate["actions"].([]string)[0] = "long_press"
	firstCandidate["bounds"].([]int)[0] = 99
	first.Details["blocking_refs"].([]string)[0] = "mutated"
	first.Details["target_actions"] = []string{"tap"}

	sourceCandidate := source.Details["candidates"].([]map[string]any)[0]
	if got := sourceCandidate["ref"]; got != "n3" {
		t.Fatalf("Public shared a candidate map with APIError: %#v", source.Details)
	}
	if got := sourceCandidate["actions"].([]string)[0]; got != "tap" {
		t.Fatalf("Public shared candidate actions with APIError: %#v", source.Details)
	}
	if got := sourceCandidate["bounds"].([]int)[0]; got != 0 {
		t.Fatalf("Public shared candidate bounds with APIError: %#v", source.Details)
	}
	if got := source.Details["blocking_refs"].([]string)[0]; got != "n4" {
		t.Fatalf("Public shared blocking_refs with APIError: %#v", source.Details)
	}
	if got := source.Details["target_actions"].([]string); len(got) != 0 {
		t.Fatalf("Public shared target_actions with APIError: %#v", source.Details)
	}
	secondCandidate := second.Details["candidates"].([]map[string]any)[0]
	if got := secondCandidate["ref"]; got != "n3" {
		t.Fatalf("two Public projections shared a candidate map: %#v", second.Details)
	}
	if got := second.Details["blocking_refs"].([]string)[0]; got != "n4" {
		t.Fatalf("two Public projections shared blocking_refs: %#v", second.Details)
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
