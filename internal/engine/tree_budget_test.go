package engine

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
	"github.com/malyjacob/mobile-use-vnext/internal/treetext"
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
			if baseline := baselineWithID(h.engine.baselines, h.engine.activeBaselineID); baseline != nil {
				baselineID = baseline.ObservationID
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
			if baseline := baselineWithID(h.engine.baselines, h.engine.activeBaselineID); baseline != nil {
				baselineAfter = baseline.ObservationID
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

type retainedHistoryEntry struct {
	state      *observationState
	cost       int
	capturedAt time.Time
	tree       *protocol.Tree
	compact    *protocol.ReadTree
	projection *Projection
}

type engineHistory struct {
	current          *observationState
	baselines        []*baselineState
	activeBaselineID string
	retained         map[string]retainedHistoryEntry
}

func fingerprintEngineHistory(e *Engine) engineHistory {
	e.mu.Lock()
	defer e.mu.Unlock()
	history := engineHistory{
		current:          e.current,
		baselines:        append([]*baselineState(nil), e.baselines...),
		activeBaselineID: e.activeBaselineID,
		retained:         make(map[string]retainedHistoryEntry, len(e.observations)),
	}
	for id, obs := range e.observations {
		if obs == nil {
			continue
		}
		history.retained[id] = retainedHistoryEntry{
			state: obs, cost: obs.Cost, capturedAt: obs.CapturedAt,
			tree: obs.tree(), compact: obs.Compact, projection: obs.Projection,
		}
	}
	return history
}

func sameRetainedHistoryEntry(left, right retainedHistoryEntry) bool {
	return left.state == right.state &&
		left.cost == right.cost &&
		left.capturedAt.Equal(right.capturedAt) &&
		left.tree == right.tree &&
		left.compact == right.compact &&
		left.projection == right.projection
}

func sameBaseline(left, right *baselineState) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ObservationID == right.ObservationID &&
		left.Origin == right.Origin &&
		left.Revision == right.Revision &&
		left.CapturedAt.Equal(right.CapturedAt) &&
		left.DisplayID == right.DisplayID &&
		left.DisplayGeneration == right.DisplayGeneration &&
		left.Tree == right.Tree &&
		left.Projection == right.Projection
}

func sameBaselinePool(left, right []*baselineState) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !sameBaseline(left[i], right[i]) {
			return false
		}
	}
	return true
}

func requireRetainedHistoryEntry(t *testing.T, e *Engine, id string,
	want retainedHistoryEntry) {
	t.Helper()
	after := fingerprintEngineHistory(e)
	got, ok := after.retained[id]
	if !ok {
		t.Fatalf("retained snapshot %s was removed", id)
	}
	if !sameRetainedHistoryEntry(got, want) {
		t.Fatalf("retained snapshot %s changed", id)
	}
}

func requireEngineHistoryUnchanged(t *testing.T, e *Engine, before engineHistory) {
	t.Helper()
	after := fingerprintEngineHistory(e)
	if after.current != before.current {
		t.Fatalf("rejected acquisition replaced current")
	}
	if !sameBaselinePool(after.baselines, before.baselines) ||
		after.activeBaselineID != before.activeBaselineID {
		t.Fatalf("rejected acquisition changed the baseline pool")
	}
	if len(after.retained) != len(before.retained) {
		t.Fatalf("rejected acquisition changed retained count: %d -> %d",
			len(before.retained), len(after.retained))
	}
	for id, want := range before.retained {
		got, ok := after.retained[id]
		if !ok {
			t.Fatalf("rejected acquisition removed retained snapshot %s", id)
		}
		if !sameRetainedHistoryEntry(got, want) {
			t.Fatalf("rejected acquisition changed retained snapshot %s", id)
		}
	}
}

func replayCurrentCapture(t *testing.T, e *Engine) captureStep {
	t.Helper()
	e.mu.Lock()
	current := e.current
	valid := current != nil && current.Projection != nil &&
		current.Projection.Capture != nil
	var replayed device.Capture
	if valid {
		replayed = *current.Projection.Capture
	}
	e.mu.Unlock()
	if !valid {
		t.Fatal("current observation has no replayable capture")
	}
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		capture := replayed
		capture.Display = display
		return capture, nil
	}
}

func currentSnapshotCost(t *testing.T, e *Engine) int {
	t.Helper()
	e.mu.Lock()
	current := e.current
	valid := current != nil && current.Projection != nil &&
		current.Projection.Capture != nil && current.Projection.Tree != nil
	var capture *device.Capture
	var tree *protocol.Tree
	var stored int
	if valid {
		capture, tree, stored = current.Projection.Capture, current.Projection.Tree, current.Cost
	}
	e.mu.Unlock()
	if !valid {
		t.Fatal("current observation has no complete snapshot")
	}
	cost, err := observation.SnapshotCost(capture, tree)
	if err != nil {
		t.Fatalf("measure current snapshot: %v", err)
	}
	if cost != stored {
		t.Fatalf("current snapshot cost = %d, stored cost = %d", cost, stored)
	}
	return cost
}

func installRetainedSnapshot(t *testing.T, e *Engine, id, status string,
	cost int, capturedAt time.Time, tree *protocol.Tree) *observationState {
	t.Helper()
	compact := observation.ProjectCompactTree(tree)
	if compact == nil {
		t.Fatalf("fixture %s has no compact tree", id)
	}
	obs := &observationState{
		ID: id, Origin: "Pfixture", Revision: 1, Agent: "agent",
		CapturedAt: capturedAt, ActionableUntil: capturedAt.Add(time.Hour),
		Projection: &Projection{Tree: tree}, Compact: compact,
		Cost: cost, Status: status, Full: true,
	}
	e.mu.Lock()
	if _, exists := e.observations[id]; exists {
		e.mu.Unlock()
		t.Fatalf("fixture observation %s already exists", id)
	}
	if e.session != nil {
		obs.SessionEpoch = e.session.Epoch
	}
	e.observations[id] = obs
	e.mu.Unlock()
	return obs
}

func installFixtureBaseline(e *Engine, obs *observationState) {
	e.mu.Lock()
	origin, revision, displayID, displayGeneration := "", 0, 0, ""
	if e.session != nil {
		origin = e.session.Origin
		revision = e.session.Revision
		displayID = e.session.Display.ID
		displayGeneration = e.session.Display.Generation
	}
	obs.Origin = origin
	obs.Revision = revision
	obs.DisplayID = displayID
	obs.DisplayGeneration = displayGeneration
	entry := &baselineState{
		ObservationID: obs.ID, Origin: origin, Revision: revision,
		CapturedAt: time.Now(), DisplayID: displayID,
		DisplayGeneration: displayGeneration,
		Tree:              obs.Compact, Projection: obs.Projection,
	}
	e.baselines = []*baselineState{entry}
	e.activeBaselineID = obs.ID
	e.mu.Unlock()
}

func retainedSnapshotCost(e *Engine) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	total := 0
	for _, obs := range e.observations {
		if obs != nil {
			total += obs.Cost
		}
	}
	return total
}

func TestCompactFullTreeBudgetBoundary(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	if start.Observation.Tree == nil || start.Observation.Tree.Full == nil {
		t.Fatalf("start did not publish a full tree")
	}
	encoded, err := json.Marshal(start.Observation.Tree.Full)
	if err != nil {
		t.Fatalf("encode compact full tree: %v", err)
	}
	exact := len(encoded)
	h.engine.cfg.CaptureMaxAge = time.Minute
	h.device.scriptCaptures(replayCurrentCapture(t, h.engine))

	h.engine.cfg.MaxFullTreeBytes = exact
	accepted, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: start.Observation.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("observe at the exact public tree budget: %v", apiErr)
	}
	if accepted.Observation == nil || accepted.Observation.Error != nil ||
		accepted.Observation.Tree == nil || accepted.Observation.Tree.Full == nil {
		t.Fatalf("expected a published full tree at the exact budget: %+v", accepted.Observation)
	}
	h.engine.mu.Lock()
	published := h.engine.current.Compact
	h.engine.mu.Unlock()
	publishedBytes, err := json.Marshal(published)
	if err != nil {
		t.Fatalf("encode published compact tree: %v", err)
	}
	if len(publishedBytes) != exact {
		t.Fatalf("published compact tree = %d bytes, want %d", len(publishedBytes), exact)
	}

	before := fingerprintEngineHistory(h.engine)
	h.engine.cfg.MaxFullTreeBytes = exact - 1
	rejected, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: start.Observation.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("observe below the public tree budget: %v", apiErr)
	}
	if rejected.Observation == nil || rejected.Observation.Error == nil {
		t.Fatalf("expected a failed observation below the budget: %+v", rejected.Observation)
	}
	if rejected.Observation.Error.Code != protocol.CodeTreeTooLarge {
		t.Fatalf("error code = %s, want %s",
			rejected.Observation.Error.Code, protocol.CodeTreeTooLarge)
	}
	if got := rejected.Observation.Error.Details["budget"]; got != "public_full" {
		t.Fatalf("details.budget = %v, want public_full", got)
	}
	if got := rejected.Observation.Error.Details["limit"]; got != exact-1 {
		t.Fatalf("details.limit = %v, want %d", got, exact-1)
	}
	requireEngineHistoryUnchanged(t, h.engine, before)
}

func TestSnapshotCostBudgetBoundary(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.engine.cfg.CaptureMaxAge = time.Minute
	h.device.scriptCaptures(replayCurrentCapture(t, h.engine))
	exact := currentSnapshotCost(t, h.engine)

	h.engine.cfg.MaxSnapshotCostBytes = exact
	accepted, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: start.Observation.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("observe at the exact snapshot cost budget: %v", apiErr)
	}
	if accepted.Observation == nil || accepted.Observation.Error != nil {
		t.Fatalf("expected a published observation at the exact snapshot budget: %+v",
			accepted.Observation)
	}
	h.engine.mu.Lock()
	current := h.engine.current
	cost, err := observation.SnapshotCost(current.Projection.Capture, current.Projection.Tree)
	stored := current.Cost
	h.engine.mu.Unlock()
	if err != nil {
		t.Fatalf("measure published snapshot: %v", err)
	}
	if cost != exact || stored != exact {
		t.Fatalf("published snapshot cost = %d, stored = %d, want %d", cost, stored, exact)
	}

	before := fingerprintEngineHistory(h.engine)
	h.engine.cfg.MaxSnapshotCostBytes = exact - 1
	rejected, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: start.Observation.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("observe below the snapshot cost budget: %v", apiErr)
	}
	if rejected.Observation == nil || rejected.Observation.Error == nil {
		t.Fatalf("expected a failed observation below the snapshot budget: %+v",
			rejected.Observation)
	}
	if rejected.Observation.Error.Code != protocol.CodeTreeTooLarge {
		t.Fatalf("error code = %s, want %s",
			rejected.Observation.Error.Code, protocol.CodeTreeTooLarge)
	}
	if got := rejected.Observation.Error.Details["budget"]; got != "internal_snapshot" {
		t.Fatalf("details.budget = %v, want internal_snapshot", got)
	}
	if got := rejected.Observation.Error.Details["limit"]; got != exact-1 {
		t.Fatalf("details.limit = %v, want %d", got, exact-1)
	}
	requireEngineHistoryUnchanged(t, h.engine, before)
}

func TestRetainedSnapshotEvictionProtectsCurrentAndClaimed(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.engine.cfg.CaptureMaxAge = time.Minute
	h.device.scriptCaptures(replayCurrentCapture(t, h.engine))
	newCost := currentSnapshotCost(t, h.engine)

	base := time.Now().Add(-10 * time.Minute)
	baseline := installRetainedSnapshot(t, h.engine, "Obudget_baseline",
		ObservationRetired, 80, base, readonlyTree(readonlyNode("n1", protocol.RoleRoot)))
	installFixtureBaseline(h.engine, baseline)
	claimedOld := installRetainedSnapshot(t, h.engine, "Obudget_claimed_old",
		ObservationClaimed, 90, base.Add(3*time.Minute),
		readonlyTree(readonlyNode("n2", protocol.RoleRoot)))
	claimedNew := installRetainedSnapshot(t, h.engine, "Obudget_claimed_new",
		ObservationClaimed, 70, base.Add(4*time.Minute),
		readonlyTree(readonlyNode("n3", protocol.RoleRoot)))
	evictOldest := installRetainedSnapshot(t, h.engine, "Obudget_evict_oldest",
		ObservationRetired, 30, base.Add(1*time.Minute),
		readonlyTree(readonlyNode("n4", protocol.RoleRoot)))
	evictMiddle := installRetainedSnapshot(t, h.engine, "Obudget_evict_middle",
		ObservationExpired, 40, base.Add(2*time.Minute),
		readonlyTree(readonlyNode("n5", protocol.RoleRoot)))
	evictNewest := installRetainedSnapshot(t, h.engine, "Obudget_evict_newest",
		ObservationRetired, 50, base.Add(5*time.Minute),
		readonlyTree(readonlyNode("n6", protocol.RoleRoot)))

	currentID := start.Observation.ID
	currentCost := newCost
	limit := baseline.Cost + currentCost + claimedOld.Cost + claimedNew.Cost +
		newCost + evictNewest.Cost
	before := fingerprintEngineHistory(h.engine)
	beforeTotal := retainedSnapshotCost(h.engine)
	if beforeTotal+newCost <= limit {
		t.Fatalf("fixture does not require eviction: total=%d new=%d limit=%d",
			beforeTotal, newCost, limit)
	}
	if beforeTotal-evictOldest.Cost-evictMiddle.Cost+newCost != limit {
		t.Fatalf("fixture eviction does not land exactly on the limit")
	}

	h.engine.cfg.MaxRetainedSnapshotCostBytes = limit
	result, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: baseline.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("observe with retained eviction: %v", apiErr)
	}
	if result.Observation == nil || result.Observation.Error != nil {
		t.Fatalf("expected a published observation after eviction: %+v", result.Observation)
	}

	after := fingerprintEngineHistory(h.engine)
	for _, id := range []string{evictOldest.ID, evictMiddle.ID} {
		if _, ok := after.retained[id]; ok {
			t.Fatalf("unprotected oldest snapshot %s survived eviction", id)
		}
	}
	for _, id := range []string{baseline.ID, currentID, claimedOld.ID, claimedNew.ID, evictNewest.ID} {
		want, ok := before.retained[id]
		if !ok {
			t.Fatalf("fixture snapshot %s is missing before eviction", id)
		}
		requireRetainedHistoryEntry(t, h.engine, id, want)
	}
	if !sameBaselinePool(after.baselines, before.baselines) {
		t.Fatalf("retained eviction changed the baseline")
	}
	for id, want := range before.retained {
		if want.state.Status != ObservationClaimed {
			continue
		}
		requireRetainedHistoryEntry(t, h.engine, id, want)
	}
	if after.current == nil || after.current.ID == currentID ||
		after.current.Cost != newCost {
		t.Fatalf("new current observation = %+v, want a new snapshot costing %d",
			after.current, newCost)
	}
	if total := retainedSnapshotCost(h.engine); total > limit {
		t.Fatalf("retained snapshot cost = %d, limit = %d", total, limit)
	}
}

func TestRetainedSnapshotFailureLeavesHistoryUnchanged(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	h.engine.cfg.CaptureMaxAge = time.Minute
	h.device.scriptCaptures(replayCurrentCapture(t, h.engine))
	newCost := currentSnapshotCost(t, h.engine)

	base := time.Now().Add(-10 * time.Minute)
	baseline := installRetainedSnapshot(t, h.engine, "Obudget_fail_baseline",
		ObservationRetired, 100, base, readonlyTree(readonlyNode("n1", protocol.RoleRoot)))
	installFixtureBaseline(h.engine, baseline)
	claimedOld := installRetainedSnapshot(t, h.engine, "Obudget_fail_claimed_old",
		ObservationClaimed, 80, base.Add(2*time.Minute),
		readonlyTree(readonlyNode("n2", protocol.RoleRoot)))
	claimedNew := installRetainedSnapshot(t, h.engine, "Obudget_fail_claimed_new",
		ObservationClaimed, 60, base.Add(3*time.Minute),
		readonlyTree(readonlyNode("n3", protocol.RoleRoot)))
	installRetainedSnapshot(t, h.engine, "Obudget_fail_unprotected",
		ObservationRetired, 20, base.Add(1*time.Minute),
		readonlyTree(readonlyNode("n4", protocol.RoleRoot)))

	currentCost := newCost
	// An expired baseline is no longer a permanent admission or retention
	// barrier. Even after pool pressure, the current observation and both claims
	// remain protected and make this limit impossible.
	protectedCost := currentCost + claimedOld.Cost + claimedNew.Cost
	limit := protectedCost + newCost - 1
	if protectedCost+newCost <= limit {
		t.Fatalf("fixture does not exceed the retained budget")
	}
	before := fingerprintEngineHistory(h.engine)
	h.engine.cfg.MaxRetainedSnapshotCostBytes = limit

	result, apiErr := h.engine.Observe(context.Background(), "agent", "",
		&protocol.ObserveRequest{Reference: baseline.ID, Full: true})
	if apiErr != nil {
		t.Fatalf("observe beyond the retained budget: %v", apiErr)
	}
	if result.Observation == nil || result.Observation.Error == nil {
		t.Fatalf("expected a failed observation beyond the retained budget: %+v",
			result.Observation)
	}
	if result.Observation.Error.Code != protocol.CodeTreeTooLarge {
		t.Fatalf("error code = %s, want %s",
			result.Observation.Error.Code, protocol.CodeTreeTooLarge)
	}
	if got := result.Observation.Error.Details["budget"]; got != "retained_snapshots" {
		t.Fatalf("details.budget = %v, want retained_snapshots", got)
	}
	if got := result.Observation.Error.Details["limit"]; got != limit {
		t.Fatalf("details.limit = %v, want %d", got, limit)
	}
	requireEngineHistoryUnchanged(t, h.engine, before)
}

func TestBaselinePoolPressureEvictsOldestUnselectedBackup(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	base := time.Now().Add(-time.Minute)
	h.engine.mu.Lock()
	current := h.engine.current
	current.Cost = 10
	tree := current.Compact
	h.engine.mu.Unlock()

	// The fixture fills every pool slot: one selected base plus the four newest
	// backups. Only the oldest backup may be released to satisfy the retained
	// snapshot budget.
	selected := installRetainedSnapshot(t, h.engine, "Opressure_selected",
		ObservationRetired, 10, base, readonlyTree(readonlyNode("n2", protocol.RoleRoot)))
	newest := installRetainedSnapshot(t, h.engine, "Opressure_newest",
		ObservationRetired, 10, base.Add(time.Second),
		readonlyTree(readonlyNode("n3", protocol.RoleRoot)))
	newer := installRetainedSnapshot(t, h.engine, "Opressure_newer",
		ObservationRetired, 10, base.Add(2*time.Second),
		readonlyTree(readonlyNode("n4", protocol.RoleRoot)))
	older := installRetainedSnapshot(t, h.engine, "Opressure_older",
		ObservationRetired, 10, base.Add(3*time.Second),
		readonlyTree(readonlyNode("n5", protocol.RoleRoot)))
	oldest := installRetainedSnapshot(t, h.engine, "Opressure_oldest",
		ObservationRetired, 20, base.Add(4*time.Second),
		readonlyTree(readonlyNode("n6", protocol.RoleRoot)))

	h.engine.mu.Lock()
	pool := []*baselineState{
		{ObservationID: selected.ID, Revision: 6, Tree: tree},
		{ObservationID: newest.ID, Revision: 5, Tree: tree},
		{ObservationID: newer.ID, Revision: 4, Tree: tree},
		{ObservationID: older.ID, Revision: 3, Tree: tree},
		{ObservationID: oldest.ID, Revision: 2, Tree: tree},
	}
	// Retained snapshots total 70 bytes (current 10 plus the five pool entries);
	// publishing 20 more needs exactly the oldest 20-byte backup released.
	h.engine.cfg.MaxRetainedSnapshotCostBytes = 70
	victims, finalPool, fits := h.engine.planRetentionWithPoolPressureLocked(
		20, pool, selected.ID)
	h.engine.mu.Unlock()
	if !fits {
		t.Fatal("pool pressure did not make the retained budget fit")
	}
	if baselineWithID(finalPool, oldest.ID) != nil {
		t.Fatalf("oldest unselected backup survived pool pressure: %+v", finalPool)
	}
	for _, retainedID := range []string{selected.ID, newest.ID, newer.ID, older.ID} {
		if baselineWithID(finalPool, retainedID) == nil {
			t.Fatalf("pool pressure removed a protected backup %s: %+v",
				retainedID, finalPool)
		}
	}
	if len(finalPool) != maxBackupBaselines {
		t.Fatalf("pressure pool length = %d, want %d", len(finalPool), maxBackupBaselines)
	}
	foundVictim := false
	for _, victim := range victims {
		if victim == nil || victim == current || victim.ID == selected.ID {
			t.Fatalf("protected observation became a pressure victim: %+v", victim)
		}
		if victim.ID == oldest.ID {
			foundVictim = true
		}
	}
	if !foundVictim {
		t.Fatalf("pool exit could not release its snapshot: %+v", victims)
	}
}

// exactRatioDeltaFixture builds one base/target pair and its structural delta
// whose canonical tree v5 blocks are exactly deltaBytes and fullBytes. The
// changed label appears in both blocks, so one more byte lengthens both alike;
// the unchanged sibling label appears only in the full block and can absorb the
// remaining difference at codepoint granularity. deltaBase fixes the one
// delta-block-only constant (the delta header's base spelling) that makes an exact
// even ratio constructible without changing the node shape. The measured lengths
// are asserted, so a tree-v5 encoding change cannot silently move the boundary.
func exactRatioDeltaFixture(t *testing.T, previewID, deltaBase string,
	deltaBytes, fullBytes int) (*protocol.ReadTree, *protocol.ReadTree, *protocol.ReadDelta) {
	t.Helper()
	probeBase := thresholdReadTree(1, 0)
	probeTree := thresholdReadTree(2, 0)
	probeDelta, err := observation.ReadDiff(deltaBase, probeBase, probeTree)
	if err != nil {
		t.Fatalf("build ratio probe: %v", err)
	}
	probeDeltaText, ok := treetext.EncodeDelta(previewID, probeDelta)
	if !ok {
		t.Fatal("encode ratio probe delta")
	}
	changedBytes := 2 + deltaBytes - len(probeDeltaText)
	if changedBytes < 1 {
		t.Fatalf("ratio target %d is smaller than the fixed delta block", deltaBytes)
	}
	for stableBytes := 0; stableBytes <= 512; stableBytes++ {
		target := thresholdReadTree(changedBytes, stableBytes)
		fullText, ok := treetext.EncodeFull(previewID, target)
		if !ok {
			t.Fatal("encode ratio full")
		}
		if len(fullText) != fullBytes {
			continue
		}
		base := thresholdReadTree(1, stableBytes)
		delta, err := observation.ReadDiff(deltaBase, base, target)
		if err != nil {
			t.Fatalf("build exact ratio delta: %v", err)
		}
		deltaText, ok := treetext.EncodeDelta(previewID, delta)
		if !ok {
			t.Fatal("encode exact ratio delta")
		}
		if len(deltaText) != deltaBytes {
			t.Fatalf("delta block = %d bytes, want %d", len(deltaText), deltaBytes)
		}
		return base, target, delta
	}
	t.Fatalf("could not construct exact %d/%d tree v5 blocks", deltaBytes, fullBytes)
	return nil, nil, nil
}

// TestDeltaCostGateExactByteRatioBoundary pins the strict "< 80%" comparison with
// the exact 799/1000 and 800/1000 tree v5 blocks from the proposal: 7990 < 8000
// publishes the delta, while 8000 >= 8000 must fall back to the full block. The
// previous 7/10 gate rejects the 799/1000 case and a non-strict `<=` comparator
// publishes the 800/1000 case, so this pair is the reverse control for both.
func TestDeltaCostGateExactByteRatioBoundary(t *testing.T) {
	const deltaBase = "OsyntheticAB"
	for _, test := range []struct {
		name       string
		deltaBytes int
		wantDelta  bool
	}{
		{name: "seven_nine_nine_over_one_thousand", deltaBytes: 799, wantDelta: true},
		{name: "eight_hundred_over_one_thousand", deltaBytes: 800, wantDelta: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, target, delta := exactRatioDeltaFixture(
				t, "Osynthetic", deltaBase, test.deltaBytes, 1000)
			fullText, ok := treetext.EncodeFull("Osynthetic", target)
			if !ok || len(fullText) != 1000 {
				t.Fatalf("full block = %d bytes, want 1000", len(fullText))
			}
			engine := &Engine{differ: fixedReadDiffer{delta: delta}}
			selected, deltaBytes, accepted := engine.treeTextBaselineDelta(
				"Osynthetic", &baselineState{ObservationID: deltaBase, Tree: base}, target)
			if accepted != test.wantDelta {
				t.Fatalf("delta accepted=%t, want %t (%d/1000 tree v5 blocks)",
					accepted, test.wantDelta, test.deltaBytes)
			}
			if !test.wantDelta {
				if selected != nil || deltaBytes != 0 {
					t.Fatalf("equal-boundary delta was measured: %+v bytes=%d",
						selected, deltaBytes)
				}
				return
			}
			if selected == nil || deltaBytes != test.deltaBytes {
				t.Fatalf("selected delta bytes = %d, want %d", deltaBytes, test.deltaBytes)
			}
		})
	}
}

func TestFullFallbackWithoutCurrentKeepsFourBackups(t *testing.T) {
	t.Run("success trims oldest", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		h.engine.cfg.CaptureMaxAge = time.Minute
		h.device.scriptCaptures(replayCurrentCapture(t, h.engine))
		oldest := installFullFallbackPool(t, h, start)

		result, apiErr := h.engine.Observe(context.Background(), "agent", "PfallbackTrim",
			&protocol.ObserveRequest{Reference: start.OperationID})
		if apiErr != nil {
			t.Fatalf("forced full fallback: %v", apiErr)
		}
		if result.Observation == nil || result.Observation.Error != nil ||
			result.Observation.Tree == nil || result.Observation.Tree.Full == nil {
			t.Fatalf("forced full fallback did not publish a full tree: %+v", result.Observation)
		}

		h.engine.mu.Lock()
		defer h.engine.mu.Unlock()
		if len(h.engine.baselines) != maxBackupBaselines {
			t.Fatalf("backup-only pool length = %d, want %d",
				len(h.engine.baselines), maxBackupBaselines)
		}
		if h.engine.activeBaselineID != "" {
			t.Fatalf("full fallback kept active baseline %q", h.engine.activeBaselineID)
		}
		if baselineWithID(h.engine.baselines, oldest.ObservationID) != nil {
			t.Fatalf("oldest backup survived full fallback: %+v", h.engine.baselines)
		}
	})

	t.Run("failure has no side effects", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		h.engine.cfg.CaptureMaxAge = time.Minute
		h.device.scriptCaptures(replayCurrentCapture(t, h.engine))
		installFullFallbackPool(t, h, start)

		before := fingerprintEngineHistory(h.engine)
		h.engine.mu.Lock()
		nextObservation := h.engine.nextObservation
		revision := h.engine.session.Revision
		h.engine.cfg.MaxRetainedSnapshotCostBytes = -1
		h.engine.mu.Unlock()

		result, apiErr := h.engine.Observe(context.Background(), "agent", "PfallbackFail",
			&protocol.ObserveRequest{Reference: start.OperationID})
		if apiErr != nil {
			t.Fatalf("forced full fallback: %v", apiErr)
		}
		if result.Observation == nil || result.Observation.Error == nil ||
			result.Observation.Error.Code != protocol.CodeTreeTooLarge {
			t.Fatalf("forced full fallback = %+v, want retained budget failure",
				result.Observation)
		}
		requireEngineHistoryUnchanged(t, h.engine, before)
		h.engine.mu.Lock()
		defer h.engine.mu.Unlock()
		if h.engine.nextObservation != nextObservation ||
			h.engine.session.Revision != revision {
			t.Fatalf("failed fallback changed identity state: O %d->%d revision %d->%d",
				nextObservation, h.engine.nextObservation, revision, h.engine.session.Revision)
		}
	})
}

func installFullFallbackPool(t *testing.T, h *harness, start *protocol.Result) *baselineState {
	t.Helper()
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	session := h.engine.session
	tree := start.Observation.Tree.Full
	nextRevision := session.Revision + 1
	// A full pool: one current baseline plus four backups. The full fallback
	// clears the active pointer, so the fourth backup slot must stay available
	// and the oldest of the five entries has to be trimmed.
	entries := []*baselineState{
		newFullFallbackBaseline("Obackup_newest", session, nextRevision-1, tree),
		newFullFallbackBaseline("Obackup_newer", session, nextRevision-2, tree),
		newFullFallbackBaseline("Obackup_middle", session, nextRevision-3, tree),
		newFullFallbackBaseline("Obackup_older", session, nextRevision-4, tree),
		newFullFallbackBaseline("Obackup_oldest", session, nextRevision-5, tree),
	}
	h.engine.baselines = entries
	h.engine.activeBaselineID = entries[0].ObservationID
	return entries[len(entries)-1]
}

func newFullFallbackBaseline(id string, session *sessionState, revision int,
	tree *protocol.ReadTree) *baselineState {
	return &baselineState{
		ObservationID: id, Origin: session.Origin, Revision: revision,
		CapturedAt: time.Now(), DisplayID: session.Display.ID,
		DisplayGeneration: session.Display.Generation, Tree: tree,
	}
}
