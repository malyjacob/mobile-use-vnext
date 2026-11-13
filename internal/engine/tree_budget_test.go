package engine

import (
	"context"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// TestTreeBudgetsNameTheirBudget proves that all three delivery budgets fail
// with the same controlled code but with distinct, minimal details, and that a
// rejected candidate publishes nothing and removes nothing.
func TestTreeBudgetsNameTheirBudget(t *testing.T) {
	cases := []struct {
		name   string
		budget string
		limit  int
		set    func(cfg *Config)
	}{
		{"public_full", "public_full", 16, func(cfg *Config) { cfg.MaxFullTreeBytes = 16 }},
		{"internal_snapshot", "internal_snapshot", 16, func(cfg *Config) { cfg.MaxSnapshotCostBytes = 16 }},
		{"retained_snapshots", "retained_snapshots", 1, func(cfg *Config) {
			cfg.MaxRetainedSnapshotCostBytes = 1
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			start := h.startSession()
			firstID := start.Observation.ID

			h.engine.mu.Lock()
			currentBefore := h.engine.current.ID
			retainedBefore := len(h.engine.observations)
			baselineID := ""
			if h.engine.baseline != nil {
				baselineID = h.engine.baseline.ObservationID
			}
			h.engine.mu.Unlock()

			testCase.set(&h.engine.cfg)
			result, apiErr := h.engine.Observe(context.Background(), "agent", "",
				&protocol.ObserveRequest{Reference: firstID})
			if apiErr != nil {
				t.Fatalf("observe rejected at admission: %v", apiErr)
			}
			if result.Observation == nil || result.Observation.Error == nil {
				t.Fatalf("expected a failed observation, got %+v", result.Observation)
			}
			if result.Observation.Error.Code != protocol.CodeTreeTooLarge {
				t.Fatalf("error code = %s, want tree_too_large", result.Observation.Error.Code)
			}
			if got := result.Observation.Error.Details["budget"]; got != testCase.budget {
				t.Fatalf("details.budget = %v, want %s", got, testCase.budget)
			}
			if got := result.Observation.Error.Details["limit"]; got != testCase.limit {
				t.Fatalf("details.limit = %v, want %d", got, testCase.limit)
			}

			h.engine.mu.Lock()
			currentAfter := h.engine.current.ID
			retainedAfter := len(h.engine.observations)
			baselineAfter := ""
			if h.engine.baseline != nil {
				baselineAfter = h.engine.baseline.ObservationID
			}
			h.engine.mu.Unlock()
			if currentAfter != currentBefore {
				t.Fatalf("a rejected candidate replaced the current observation: %s -> %s",
					currentBefore, currentAfter)
			}
			if retainedAfter != retainedBefore {
				t.Fatalf("a rejected candidate changed the retained observation count: %d -> %d",
					retainedBefore, retainedAfter)
			}
			// The admitted observe already confirmed the baseline; a rejected
			// candidate never rolls that acknowledgement back.
			if baselineAfter != firstID || baselineID != "" {
				t.Fatalf("baseline confirmation was rolled back: before=%q after=%q want=%q",
					baselineID, baselineAfter, firstID)
			}

			// Restoring the budget publishes exactly the tree the discarded
			// candidate would have produced: the confirmed baseline is unchanged,
			// so the delta is empty.
			h.engine.cfg = DefaultConfig(h.engine.cfg.ServiceInstanceID, h.engine.cfg.AgentName)
			h.engine.cfg.MaxObservations = 32
			again, apiErr := h.engine.Observe(context.Background(), "agent", "",
				&protocol.ObserveRequest{Reference: firstID})
			if apiErr != nil {
				t.Fatalf("observe after budget restore: %v", apiErr)
			}
			if again.Observation == nil || again.Observation.Tree == nil ||
				again.Observation.Tree.Delta == nil {
				t.Fatalf("expected an empty delta from the confirmed baseline: %+v", again.Observation)
			}
			if !again.Observation.Tree.Delta.IsEmpty() {
				t.Fatalf("a rejected candidate changed ref continuity: %+v",
					again.Observation.Tree.Delta)
			}
		})
	}
}
