package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// addTask installs one live task fact on the fake device. A task added through
// this helper carries a proven Android user (0 unless the test set one); tests
// that need an unproven user append to the device state directly.
func (h *harness) addTask(task device.Task) {
	if !task.HasUserID {
		task.HasUserID = true
	}
	h.device.mu.Lock()
	h.device.tasks = append(h.device.tasks, task)
	h.device.mu.Unlock()
}

// ownTask registers one already-proven session window.
func (h *harness) ownTask(task device.Task, ownership string, origin *int) {
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	h.engine.session.Tasks = append(h.engine.session.Tasks, taskEntry{
		TaskID: task.TaskID, RootID: task.RootID, Package: task.Package,
		DisplayID: task.DisplayID, Generation: h.engine.session.Display.Generation,
		Ownership: ownership, OriginDisplay: origin, Proven: true,
	})
}

func appListRequest(o *protocol.Observation) *protocol.AppRequest {
	return &protocol.AppRequest{Kind: protocol.AppKindList, Reference: o.ID}
}

func TestAppListReportsLiveTasksWithoutConsumingObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"})
	h.ownTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"}, TaskOwned, nil)
	h.addTask(device.Task{TaskID: 88, RootID: 88, DisplayID: 12, Package: "com.other"})
	h.device.mu.Lock()
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.example.app", TaskID: intPtr(77)}
	h.device.mu.Unlock()
	h.engine.mu.Lock()
	expires := h.engine.session.ExpiresAt
	h.engine.mu.Unlock()

	result, apiErr := h.engine.App(context.Background(), "agent", "Plist", appListRequest(first.Observation))
	if apiErr != nil {
		t.Fatalf("app list: %v", apiErr)
	}
	if result.App == nil || result.App.Kind != protocol.AppKindList ||
		intValue(t, result.App.DisplayID, "display_id") != 12 {
		t.Fatalf("app=%+v", result.App)
	}
	if result.Outcome != "" || result.Error != nil {
		t.Fatalf("query must not carry an outcome: %+v", result)
	}
	if len(result.App.TaskItems) != 2 || result.App.TaskItems[0].TaskID != 77 ||
		result.App.TaskItems[0].Ownership != TaskOwned ||
		result.App.TaskItems[1].Ownership != protocol.TaskOwnershipShared {
		t.Fatalf("items=%+v", result.App.TaskItems)
	}
	if result.App.Foreground == nil || result.App.Foreground.State != device.ForegroundPresent ||
		result.App.Foreground.TaskID == nil || *result.App.Foreground.TaskID != 77 {
		t.Fatalf("foreground=%+v", result.App.Foreground)
	}
	h.engine.mu.Lock()
	stillReady := h.engine.observations[first.Observation.ID].Status == ObservationReady
	unchanged := h.engine.session.ExpiresAt.Equal(expires)
	h.engine.mu.Unlock()
	if !stillReady {
		t.Fatalf("list consumed the ready observation")
	}
	if !unchanged {
		t.Fatalf("list renewed the session lease")
	}
}

func TestAppListInventoryFailureOmitsForeground(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.device.mu.Lock()
	h.device.taskErr = context.DeadlineExceeded
	h.device.mu.Unlock()
	result, _ := h.engine.App(context.Background(), "agent", "Plist", appListRequest(first.Observation))
	if result.Error == nil || result.Error.Code != protocol.CodeDeviceUnavailable {
		t.Fatalf("expected a controlled inventory failure: %+v", result)
	}
	if result.App == nil || result.App.Foreground != nil || result.App.TaskItems != nil ||
		result.App.HasItems {
		t.Fatalf("a failed inventory must not deliver a foreground or collection: %+v", result.App)
	}
	if result.Outcome != "" {
		t.Fatalf("failed query must not carry an outcome")
	}
}

func TestAppQueryCancelledClosesWithoutOutcome(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, apiErr := h.engine.App(ctx, "agent", "Pfindcancel", appFindRequest("", false))
	if apiErr != nil {
		t.Fatalf("cancelled find: %v", apiErr)
	}
	if result.Phase != "query_cancelled" || result.Outcome != "" || result.Error != nil ||
		result.App == nil || result.App.Kind != protocol.AppKindFind {
		t.Fatalf("cancelled find=%+v", result)
	}
	listed, apiErr := h.engine.App(ctx, "agent", "Plistcancel", appListRequest(first.Observation))
	if apiErr != nil {
		t.Fatalf("cancelled list: %v", apiErr)
	}
	if listed.Phase != "query_cancelled" || listed.Outcome != "" ||
		listed.App == nil || listed.App.TaskItems != nil {
		t.Fatalf("cancelled list=%+v", listed)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.work != nil {
		t.Fatalf("cancelled query kept the work position")
	}
	if h.engine.observations[first.Observation.ID].Status != ObservationReady {
		t.Fatalf("cancelled list consumed the observation")
	}
}

func TestAppClaimMovesPrimaryTaskAsBorrowed(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 41, RootID: 41, DisplayID: 0, Package: "com.example.app",
		Component: "com.example.app/.Main"})
	h.device.mu.Lock()
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.example.app", TaskID: intPtr(41)}
	h.device.mu.Unlock()

	result, apiErr := h.engine.App(context.Background(), "agent", "Pclaim",
		&protocol.AppRequest{Kind: protocol.AppKindClaim, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 41, UseTask: true}})
	if apiErr != nil {
		t.Fatalf("app claim: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || result.App == nil ||
		result.App.Kind != protocol.AppKindClaim ||
		result.App.Ownership != TaskBorrowed ||
		intValue(t, result.App.SourceDisplayID, "source_display_id") != 0 ||
		intValue(t, result.App.DisplayID, "display_id") != 12 ||
		intValue(t, result.App.TaskID, "task_id") != 41 {
		t.Fatalf("claim result=%+v", result)
	}
	if result.App.StatePreserved == nil || !*result.App.StatePreserved {
		t.Fatalf("same task and component should report preserved: %+v", result.App)
	}
	h.device.mu.Lock()
	moves := append([]moveCall(nil), h.device.moveCalls...)
	h.device.mu.Unlock()
	if len(moves) != 1 || moves[0].rootID != 41 || moves[0].leafID != 41 || moves[0].targetID != 12 {
		t.Fatalf("move calls=%+v", moves)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	entry := h.engine.sessionTaskLocked(h.engine.session, 41)
	if entry == nil || entry.Ownership != TaskBorrowed || entry.OriginDisplay == nil || *entry.OriginDisplay != 0 {
		t.Fatalf("registry=%+v", entry)
	}
	if len(h.engine.transfers) != 0 {
		t.Fatalf("confirmed claim must release its transfer record: %+v", h.engine.transfers)
	}
}

func TestAppClaimAmbiguousPackageKeepsObservation(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 41, RootID: 41, DisplayID: 0, Package: "com.example"})
	h.addTask(device.Task{TaskID: 42, RootID: 42, DisplayID: 0, Package: "com.example"})
	result, _ := h.engine.App(context.Background(), "agent", "Pclaim",
		&protocol.AppRequest{Kind: protocol.AppKindClaim, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{App: "com.example"}})
	if result.Error == nil || result.Error.Code != protocol.CodeTaskAmbiguous ||
		result.Outcome != OutcomeNotExecuted {
		t.Fatalf("ambiguous claim result=%+v", result)
	}
	ids, ok := result.Error.Details["task_ids"].([]int)
	if !ok || len(ids) != 2 || ids[0] != 41 || ids[1] != 42 {
		t.Fatalf("task_ids detail=%+v", result.Error.Details)
	}
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.observations[first.Observation.ID].Status != ObservationReady {
		t.Fatalf("pre-claim ambiguity consumed the observation")
	}
}

func TestAppClaimSharedRootIsRefused(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 41, RootID: 100, DisplayID: 0, Package: "com.example"})
	h.addTask(device.Task{TaskID: 42, RootID: 100, DisplayID: 0, Package: "com.other"})
	result, _ := h.engine.App(context.Background(), "agent", "Pclaim",
		&protocol.AppRequest{Kind: protocol.AppKindClaim, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 41, UseTask: true}})
	if result.Error == nil || result.Error.Code != protocol.CodeTaskRootShared ||
		result.Outcome != OutcomeNotExecuted {
		t.Fatalf("shared root result=%+v", result)
	}
}

func TestAppClaimUnknownReceiptStaysUncertainAndProtected(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 41, RootID: 41, DisplayID: 0, Package: "com.example.app"})
	unknown := device.DispatchUnknown
	h.device.mu.Lock()
	h.device.moveReceipt = &unknown
	h.device.mu.Unlock()
	result, _ := h.engine.App(context.Background(), "agent", "Pclaim",
		&protocol.AppRequest{Kind: protocol.AppKindClaim, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 41, UseTask: true}})
	if result.Outcome != OutcomeUncertain || result.Error == nil ||
		result.Error.Code != protocol.CodeTaskMoveUnconfirmed {
		t.Fatalf("unknown claim result=%+v", result)
	}
	if result.App.DisplayID != nil || result.App.Ownership != "" {
		t.Fatalf("unproven landing must omit display/ownership: %+v", result.App)
	}
	if !result.App.HasStatePreserved || result.App.StatePreserved != nil {
		t.Fatalf("claim uncertain must state state_preserved as null: %+v", result.App)
	}
	h.engine.mu.Lock()
	protects := h.engine.transferProtectsLocked(41)
	h.engine.mu.Unlock()
	if !protects {
		t.Fatalf("unknown inbound transfer must stay protected")
	}
	if err := h.engine.persistSync(context.Background()); err != nil {
		t.Fatalf("persist: %v", err)
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if h.store.record == nil || len(h.store.record.TaskTransfers) != 1 ||
		h.store.record.TaskTransfers[0].State != TransferUnknown {
		t.Fatalf("ledger transfers=%+v", h.store.record)
	}
}

func TestAppHandoffMovesTaskBackAndDropsOwnership(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app",
		Component: "com.example.app/.Main"})
	h.ownTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"}, TaskOwned, nil)
	result, apiErr := h.engine.App(context.Background(), "agent", "Phandoff",
		&protocol.AppRequest{Kind: protocol.AppKindHandoff, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 77, UseTask: true}})
	if apiErr != nil {
		t.Fatalf("app handoff: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || result.App.Ownership != TaskShared ||
		intValue(t, result.App.SourceDisplayID, "source_display_id") != 12 ||
		intValue(t, result.App.DisplayID, "display_id") != 0 {
		t.Fatalf("handoff result=%+v", result)
	}
	if result.App.ForegroundRestore != protocol.ForegroundRestoreNotRequested {
		t.Fatalf("foreground_restore=%q", result.App.ForegroundRestore)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if entry := h.engine.sessionTaskLocked(h.engine.session, 77); entry != nil {
		t.Fatalf("handed-off task stayed in the delete list: %+v", entry)
	}
	for _, transfer := range h.engine.transfers {
		if transfer.record.LeafTaskID == 77 {
			t.Fatalf("confirmed handoff kept a protection record: %+v", transfer.record)
		}
	}
}

func TestAppCloseRemovesExactOwnedTask(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"})
	h.ownTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"}, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.recentIDs = map[int]bool{}
	h.device.recentKnown = true
	h.device.mu.Unlock()
	result, apiErr := h.engine.App(context.Background(), "agent", "Pclose",
		&protocol.AppRequest{Kind: protocol.AppKindClose, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 77, UseTask: true}})
	if apiErr != nil {
		t.Fatalf("app close: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || !result.App.HasTasks ||
		len(result.App.Tasks) != 1 || result.App.Tasks[0].Outcome != OutcomeExecuted ||
		result.App.Tasks[0].RecentsCleaned == nil || !*result.App.Tasks[0].RecentsCleaned {
		t.Fatalf("close result=%+v", result.App)
	}
	if result.App.Complete == nil || !*result.App.Complete {
		t.Fatalf("complete=%v", result.App.Complete)
	}
}

func TestAppCloseBorrowedRefusedBeforeClaim(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"})
	h.ownTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"}, TaskBorrowed, intPtr(0))
	result, _ := h.engine.App(context.Background(), "agent", "Pclose",
		&protocol.AppRequest{Kind: protocol.AppKindClose, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 77, UseTask: true}})
	if result.Error == nil || result.Error.Code != protocol.CodeCloseBorrowed ||
		result.App.HasTasks {
		t.Fatalf("borrowed close result=%+v", result)
	}
	h.device.mu.Lock()
	removes := len(h.device.removeCalls)
	h.device.mu.Unlock()
	if removes != 0 {
		t.Fatalf("refused close dispatched %d removals", removes)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.observations[first.Observation.ID].Status != ObservationReady {
		t.Fatalf("refused close consumed the observation")
	}
}

func TestAppCloseAboveCapacityRefusedBeforeClaim(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	for i := 0; i < 17; i++ {
		task := device.Task{TaskID: 100 + i, RootID: 100 + i, DisplayID: 12, Package: "com.example"}
		h.addTask(task)
		h.ownTask(task, TaskOwned, nil)
	}
	result, _ := h.engine.App(context.Background(), "agent", "Pclose",
		&protocol.AppRequest{Kind: protocol.AppKindClose, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{App: "com.example"}})
	if result.Error == nil || result.Error.Code != protocol.CodeAppCapacity ||
		result.App.HasTasks || result.App.Complete != nil {
		t.Fatalf("capacity result=%+v", result)
	}
	h.device.mu.Lock()
	removes := len(h.device.removeCalls)
	h.device.mu.Unlock()
	if removes != 0 {
		t.Fatalf("capacity refusal truncated and removed %d tasks", removes)
	}
}

func TestAppClosePartialReportsPerItemFacts(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	one := device.Task{TaskID: 71, RootID: 71, DisplayID: 12, Package: "com.example"}
	two := device.Task{TaskID: 72, RootID: 72, DisplayID: 12, Package: "com.example"}
	h.addTask(one)
	h.addTask(two)
	h.ownTask(one, TaskOwned, nil)
	h.ownTask(two, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.removeScript = []device.DispatchReceipt{device.DispatchTerminated, device.DispatchUnknown}
	h.device.recentIDs = map[int]bool{}
	h.device.recentKnown = true
	h.device.mu.Unlock()
	result, _ := h.engine.App(context.Background(), "agent", "Pclose",
		&protocol.AppRequest{Kind: protocol.AppKindClose, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{App: "com.example"}})
	if result.Outcome != OutcomeUncertain || result.Error == nil ||
		result.Error.Code != protocol.CodeAppTaskUnconfirmed {
		t.Fatalf("partial close result=%+v", result)
	}
	if len(result.App.Tasks) != 2 ||
		result.App.Tasks[0].Outcome != OutcomeExecuted ||
		result.App.Tasks[1].Outcome != OutcomeUncertain {
		t.Fatalf("tasks=%+v", result.App.Tasks)
	}
	if result.App.Complete == nil || *result.App.Complete {
		t.Fatalf("partial close must not report complete")
	}
	if h.device.removeCalls[0] != 71 || len(h.device.removeCalls) != 2 {
		t.Fatalf("remove order=%v", h.device.removeCalls)
	}
}

func TestTransferPoolExcludesLiveScanAndDeletes(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	h.addTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"})
	h.engine.mu.Lock()
	h.engine.transfers = append(h.engine.transfers, &transferState{record: LedgerTransfer{
		OperationID: "Pmove", Direction: TransferOut, State: TransferUnknown,
		LeafTaskID: 77, RootTaskID: 77, Package: "com.example.app",
		SourceDisplayID: 12, TargetDisplayID: 0,
	}})
	h.engine.mu.Unlock()
	tasksOK, recentsOK, checked := h.engine.removeOwnedTasksIdentity(context.Background(), 12,
		[]taskEntry{{TaskID: 77, DisplayID: 12, Proven: true}}, device.BootIdentity{}, 0)
	if tasksOK || recentsOK {
		t.Fatalf("protected task must keep the closure unconfirmed: %v %v", tasksOK, recentsOK)
	}
	if len(checked) != 1 || checked[0] != 77 {
		t.Fatalf("checked=%v", checked)
	}
	h.device.mu.Lock()
	removes := len(h.device.removeCalls)
	h.device.mu.Unlock()
	if removes != 0 {
		t.Fatalf("live-scan adoption deleted a transfer-protected task: %d", removes)
	}
}

// OwnedProven marks a test entry as already proven.
func (e taskEntry) OwnedProven() taskEntry {
	e.Proven = true
	return e
}

func TestAppDuplicatePRepublishesOnlyKind(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 41, RootID: 41, DisplayID: 0, Package: "com.example.app"})
	request := &protocol.AppRequest{Kind: protocol.AppKindClaim, Reference: first.Observation.ID,
		Target: &protocol.AppTarget{TaskID: 41, UseTask: true}}
	result, _ := h.engine.App(context.Background(), "agent", "Pdupclaim", request)
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("first claim=%+v", result)
	}
	duplicate, apiErr := h.engine.App(context.Background(), "agent", "Pdupclaim", request)
	if apiErr != nil {
		t.Fatalf("duplicate: %v", apiErr)
	}
	if duplicate.App == nil || duplicate.App.Kind != protocol.AppKindClaim ||
		duplicate.App.TaskID != nil {
		t.Fatalf("duplicate summary must carry only app.kind: %+v", duplicate)
	}
}
func TestAppListAcceptsOperationReference(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	opID := first.OperationID
	h.addTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"})
	result, apiErr := h.engine.App(context.Background(), "agent", "Plistbyop",
		&protocol.AppRequest{Kind: protocol.AppKindList, Reference: opID})
	if apiErr != nil {
		t.Fatalf("list by P: %v", apiErr)
	}
	if result.Error != nil || result.App == nil || !result.App.HasItems ||
		intValue(t, result.App.DisplayID, "display_id") != 12 {
		t.Fatalf("list by P result=%+v", result)
	}
	unknown, _ := h.engine.App(context.Background(), "agent", "Plistbyunknown",
		&protocol.AppRequest{Kind: protocol.AppKindList, Reference: "Pmissing"})
	if unknown.Error == nil || unknown.Error.Code != protocol.CodeOperationUnknown {
		t.Fatalf("unknown P result=%+v", unknown)
	}
	other, _ := h.engine.App(context.Background(), "other", "Plistbyother",
		&protocol.AppRequest{Kind: protocol.AppKindList, Reference: opID})
	if other.Error == nil || other.Error.Code != protocol.CodeOperationNotFound {
		t.Fatalf("foreign P result=%+v", other)
	}
}
func TestRetiredOversizedRecordCompressesToOwnerSummary(t *testing.T) {
	h := newHarness(t)
	h.engine.mu.Lock()
	session := &sessionState{
		Origin: "Pcompress", Agent: "agent", Mode: "virtual",
		Display: device.Display{ID: 12, Generation: "gen"}, StartedAt: time.Now(),
		EndedAt: time.Now(), Reason: "client_end", State: SessionEnded,
	}
	for i := 0; i < 16; i++ {
		session.Tasks = append(session.Tasks, taskEntry{
			TaskID: 100 + i, Package: strings.Repeat("x", 1500),
			DisplayID: 12, Ownership: TaskOwned, Proven: true,
		})
	}
	cleanup := &cleanupState{
		State: protocol.CleanupPending, Execution: protocol.CleanupUnconfirmed,
		Tasks: protocol.CleanupPending, Recents: protocol.CleanupPending,
		Display: protocol.CleanupPending, StartedAt: time.Now(),
	}
	h.engine.retired = append(h.engine.retired, &retiredState{
		session: session, cleanup: cleanup, service: "instance-test",
	})
	h.engine.trimRetiredLocked()
	compressed := h.engine.retired[0].compressed
	dropped := h.engine.droppedUnconfirmed
	state := cleanup.State
	left := len(session.Tasks)
	encoded, _ := json.Marshal(h.engine.retired[0].ledger())
	h.engine.mu.Unlock()
	if !compressed {
		t.Fatalf("oversized retired record was not compressed")
	}
	if left != 0 || state != protocol.CleanupUnconfirmed {
		t.Fatalf("compressed record kept delete targets or a live cleanup: tasks=%d state=%s", left, state)
	}
	if dropped != 1 {
		t.Fatalf("dropped_unconfirmed=%d, want 1", dropped)
	}
	if len(encoded) > 1024 {
		t.Fatalf("owner summary is not bounded: %d bytes", len(encoded))
	}
}
func TestAppClaimRefusedForRegisteredTask(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := device.Task{TaskID: 41, RootID: 41, DisplayID: 0, Package: "com.example.app"}
	h.addTask(task)
	h.ownTask(device.Task{TaskID: 41, RootID: 41, DisplayID: 12, Package: "com.example.app"}, TaskOwned, nil)
	result, _ := h.engine.App(context.Background(), "agent", "Pclaimbusy",
		&protocol.AppRequest{Kind: protocol.AppKindClaim, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 41, UseTask: true}})
	if result.Error == nil || result.Error.Code != protocol.CodeTaskLifecycleBusy ||
		result.Outcome != OutcomeNotExecuted {
		t.Fatalf("registered task claim result=%+v", result)
	}
	h.engine.mu.Lock()
	ready := h.engine.observations[first.Observation.ID].Status == ObservationReady
	h.engine.mu.Unlock()
	if !ready {
		t.Fatalf("busy claim consumed the observation")
	}
}
