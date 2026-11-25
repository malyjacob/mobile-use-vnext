package protocol

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestObservationClassificationWire(t *testing.T) {
	observation := &Observation{
		Status:     "complete",
		Visual:     "loading",
		TreeChange: TreeChangeDetailOnly,
		Hint:       HintWaitFor,
		Tree:       &ReadTreeValue{Full: compactTreeWithChild(RoleButton, PointBounds(5, 10))},
	}
	wire := string(marshal(t, observation))
	previous := -1
	for _, member := range []string{`"visual":"loading"`, `"tree_change":"detail_only"`, `"hint":"wait_for"`, `"tree":`} {
		position := strings.Index(wire, member)
		if position <= previous {
			t.Fatalf("member %s missing or out of order in %s", member, wire)
		}
		previous = position
	}
	var decoded Observation
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatalf("decode observation: %v", err)
	}
	if decoded.Visual != "loading" || decoded.TreeChange != "detail_only" || decoded.Hint != "wait_for" {
		t.Fatalf("decoded visual, tree_change, hint = %q, %q, %q; want loading, detail_only, wait_for", decoded.Visual, decoded.TreeChange, decoded.Hint)
	}

	observation.Visual = ""
	observation.TreeChange = ""
	observation.Hint = ""
	wire = string(marshal(t, observation))
	for _, member := range []string{`"visual":`, `"tree_change":`, `"hint":`} {
		if strings.Contains(wire, member) {
			t.Fatalf("empty member %s was encoded in %s", member, wire)
		}
	}
}

func TestObservationClassificationRejectsNullAndEmpty(t *testing.T) {
	for _, field := range []string{"visual", "tree_change", "hint"} {
		for _, tc := range []struct {
			name  string
			value string
			want  string
		}{
			{"null", "null", "null is not allowed for this member"},
			{"empty", `""`, "observation." + field + " must not be empty"},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				body := `{"status":"complete","` + field + `":` + tc.value + `}`
				var observation Observation
				err := json.Unmarshal([]byte(body), &observation)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("decode %s: got %v, want error containing %q", body, err, tc.want)
				}
			})
		}
	}
}

func TestObservationClassificationValidators(t *testing.T) {
	for _, tc := range []struct {
		name   string
		valid  func(string) bool
		accept []string
		reject []string
	}{
		{
			name:   "visual",
			valid:  ValidObservationVisual,
			accept: []string{"loading", "media_surface", "sparse_tree", "image_heavy(45%)", "image_heavy(72%)", "image_heavy(100%)"},
			reject: []string{"", "unknown", "image_heavy(44%)", "image_heavy(101%)", "image_heavy(045%)", "image_heavy(45)", "image_heavy(+45%)", "image_heavy(４５%)", "image_heavy(45%)extra"},
		},
		{
			name:   "tree_change",
			valid:  ValidTreeChange,
			accept: []string{"unchanged", "detail_only", "matched_base"},
			reject: []string{"", "changed", "Unchanged", "snap"},
		},
		{
			name:   "hint",
			valid:  ValidObservationHint,
			accept: []string{"snap", "wait_for"},
			reject: []string{"", "observe", "wait-for", "loading"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, value := range tc.accept {
				if !tc.valid(value) {
					t.Errorf("rejected valid value %q", value)
				}
			}
			for _, value := range tc.reject {
				if tc.valid(value) {
					t.Errorf("accepted invalid value %q", value)
				}
			}
		})
	}
}
