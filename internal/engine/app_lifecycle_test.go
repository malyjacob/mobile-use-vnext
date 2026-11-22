package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// addTask installs one live task fact on the fake device.
func (h *harness) addTask(task device.Task) {
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

// protectTask installs one unresolved transfer responsibility for a task id.
func (h *killHarness) protectTask(taskID int, pkg string) {
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	h.engine.transfers = append(h.engine.transfers, &transferState{record: LedgerTransfer{
		OperationID: "Ptransfer", Direction: TransferOut, State: TransferUnknown,
		LeafTaskID: taskID, RootTaskID: taskID, Package: pkg,
		SourceDisplayID: 12, TargetDisplayID: 0,
	}})
}

// killRequest builds the exact kill request of one task id.
func killRequest(o *protocol.Observation, p string, taskID int) (string, *protocol.AppRequest) {
	return p, &protocol.AppRequest{Kind: protocol.AppKindKill, Reference: o.ID,
		Target: &protocol.AppTarget{TaskID: taskID, UseTask: true}}
}

func TestAppKillRefusesUnownedTask(t *testing.T) {
	h := newKillHarness(t)
	first := h.startSession()
	h.addTask(device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"})
	p, request := killRequest(first.Observation, "Pkillunowned", 77)
	result, _ := h.engine.App(context.Background(), "agent", p, request)
	if result.Error == nil || result.Error.Code != protocol.CodeKillNotOwned ||
		result.Outcome != OutcomeNotExecuted {
		t.Fatalf("unowned kill result=%+v", result)
	}
	h.device.mu.Lock()
	kills, removes := len(h.device.killCalls), len(h.device.removeCalls)
	h.device.mu.Unlock()
	if kills != 0 || removes != 0 {
		t.Fatalf("unowned kill dispatched kills=%d removes=%d", kills, removes)
	}
}

// TestAppKillRefusedWhenSamePackageTaskOnPrimaryDisplay keeps the cross-display
// protection: a primary-display task of the same package still forbids the kill.
func TestAppKillRefusedWhenSamePackageTaskOnPrimaryDisplay(t *testing.T) {
	h := newKillHarness(t)
	first := h.startSession()
	task := device.Task{TaskID: 201, RootID: 201, DisplayID: 12, Package: "com.netease.cloudmusic"}
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.addTask(device.Task{TaskID: 90, RootID: 90, DisplayID: 0, Package: "com.netease.cloudmusic"})
	h.device.mu.Lock()
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.netease.cloudmusic", TaskID: intPtr(201)}
	h.device.processes = device.PackageProcesses{UserID: 10123, Instances: []device.ProcessInstance{
		{PID: 731, StartTicks: 55, Name: "com.netease.cloudmusic"},
	}}
	h.device.mu.Unlock()

	p, request := killRequest(first.Observation, "Pkillelsewhere", 201)
	result, _ := h.engine.App(context.Background(), "agent", p, request)
	if result.Error == nil || result.Error.Code != protocol.CodeKillTaskElsewhere ||
		result.Outcome != OutcomeNotExecuted {
		t.Fatalf("other-display kill result=%+v", result)
	}
	h.device.mu.Lock()
	kills, removes := len(h.device.killCalls), len(h.device.removeCalls)
	h.device.mu.Unlock()
	if kills != 0 || removes != 0 {
		t.Fatalf("refused kill dispatched kills=%d removes=%d", kills, removes)
	}
	h.engine.mu.Lock()
	entry := h.engine.sessionTaskLocked(h.engine.session, 201)
	ready := h.engine.observations[first.Observation.ID].Status == ObservationReady
	h.engine.mu.Unlock()
	if entry == nil {
		t.Fatalf("refused kill dropped the registration")
	}
	if !ready {
		t.Fatalf("refused kill consumed the observation")
	}
}

// TestAppKillRemoveUnknownStaysUncertainAndKeepsRegistration proves the second
// step never invents executed and never drops the registration on an unknown
// removal receipt.
func TestAppKillRemoveUnknownStaysUncertainAndKeepsRegistration(t *testing.T) {
	h := newKillHarness(t)
	first := h.startSession()
	task := device.Task{TaskID: 201, RootID: 201, DisplayID: 12, Package: "com.netease.cloudmusic"}
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	unknown := device.DispatchUnknown
	h.device.mu.Lock()
	h.device.processes = device.PackageProcesses{UserID: 10123, Instances: []device.ProcessInstance{
		{PID: 731, StartTicks: 55, Name: "com.netease.cloudmusic"},
	}}
	h.device.removeReceipt = &unknown
	h.device.mu.Unlock()

	p, request := killRequest(first.Observation, "Pkillremoveunknown", 201)
	result, _ := h.engine.App(context.Background(), "agent", p, request)
	if result.Outcome != OutcomeUncertain || result.Error == nil ||
		result.Error.Code != protocol.CodeKillUnconfirmed {
		t.Fatalf("unknown removal result=%+v", result)
	}
	if result.Error.Message != "已终止应用进程，但无法确认任务窗口已移除" {
		t.Fatalf("unknown removal message=%q", result.Error.Message)
	}
	if !result.App.HasKilledPIDs || len(result.App.KilledPIDs) != 1 || result.App.KilledPIDs[0] != 731 {
		t.Fatalf("kill facts=%+v", result.App)
	}
	h.engine.mu.Lock()
	entry := h.engine.sessionTaskLocked(h.engine.session, 201)
	h.engine.mu.Unlock()
	if entry == nil {
		t.Fatalf("unconfirmed removal dropped the registration")
	}
	h.device.mu.Lock()
	live := append([]device.Task(nil), h.device.tasks...)
	h.device.mu.Unlock()
	found := false
	for _, surviving := range live {
		if surviving.TaskID == 201 {
			found = true
		}
	}
	if !found {
		t.Fatalf("an unknown removal must not be reported as a removed shell: %+v", live)
	}
}

// TestAppKillSkipsRemovalWhenShellAlreadyGone proves the pre-check: once Android
// destroyed the TaskRecord with the last process no removal is dispatched.
func TestAppKillSkipsRemovalWhenShellAlreadyGone(t *testing.T) {
	h := newKillHarness(t)
	h.script.dropTaskWhenProcessGone = 201
	first := h.startSession()
	task := device.Task{TaskID: 201, RootID: 201, DisplayID: 12, Package: "com.netease.cloudmusic"}
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.processes = device.PackageProcesses{UserID: 10123, Instances: []device.ProcessInstance{
		{PID: 731, StartTicks: 55, Name: "com.netease.cloudmusic"},
	}}
	h.device.mu.Unlock()

	p, request := killRequest(first.Observation, "Pkillshellgone", 201)
	result, _ := h.engine.App(context.Background(), "agent", p, request)
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("shell-gone kill outcome=%q error=%+v", result.Outcome, result.Error)
	}
	h.device.mu.Lock()
	kills := append([]int(nil), h.device.killCalls...)
	removes := append([]int(nil), h.device.removeCalls...)
	live := append([]device.Task(nil), h.device.tasks...)
	h.device.mu.Unlock()
	if len(kills) != 1 || kills[0] != 731 {
		t.Fatalf("kill calls=%v want [731]", kills)
	}
	if len(removes) != 0 {
		t.Fatalf("a gone task shell must not be removed again: %v", removes)
	}
	for _, surviving := range live {
		if surviving.TaskID == 201 {
			t.Fatalf("task shell survived: %+v", live)
		}
	}
	h.engine.mu.Lock()
	entry := h.engine.sessionTaskLocked(h.engine.session, 201)
	h.engine.mu.Unlock()
	if entry != nil {
		t.Fatalf("killed task stayed registered: %+v", entry)
	}
}

// TestAppKillTransferProtectionFollowsOwnership proves the guard order: the
// ownership decision comes before the transfer protection.
func TestAppKillTransferProtectionFollowsOwnership(t *testing.T) {
	t.Run("unowned is not lifecycle busy", func(t *testing.T) {
		h := newKillHarness(t)
		first := h.startSession()
		h.addTask(device.Task{TaskID: 201, RootID: 201, DisplayID: 12, Package: "com.example.app"})
		h.protectTask(201, "com.example.app")
		p, request := killRequest(first.Observation, "Pkilltransferunowned", 201)
		result, _ := h.engine.App(context.Background(), "agent", p, request)
		if result.Error == nil || result.Error.Code != protocol.CodeKillNotOwned ||
			result.Outcome != OutcomeNotExecuted {
			t.Fatalf("unowned transfer kill result=%+v", result)
		}
	})
	t.Run("owned is lifecycle busy", func(t *testing.T) {
		h := newKillHarness(t)
		first := h.startSession()
		task := device.Task{TaskID: 201, RootID: 201, DisplayID: 12, Package: "com.example.app"}
		h.addTask(task)
		h.ownTask(task, TaskOwned, nil)
		h.protectTask(201, "com.example.app")
		p, request := killRequest(first.Observation, "Pkilltransferowned", 201)
		result, _ := h.engine.App(context.Background(), "agent", p, request)
		if result.Error == nil || result.Error.Code != protocol.CodeTaskLifecycleBusy ||
			result.Outcome != OutcomeNotExecuted {
			t.Fatalf("owned transfer kill result=%+v", result)
		}
		h.device.mu.Lock()
		kills, removes := len(h.device.killCalls), len(h.device.removeCalls)
		h.device.mu.Unlock()
		if kills != 0 || removes != 0 {
			t.Fatalf("busy kill dispatched kills=%d removes=%d", kills, removes)
		}
		h.engine.mu.Lock()
		ready := h.engine.observations[first.Observation.ID].Status == ObservationReady
		h.engine.mu.Unlock()
		if !ready {
			t.Fatalf("busy kill consumed the observation")
		}
	})
}

// TestAppKillEmptyProcessSetRemovalOutcome covers the zero-signal branches: the
// removal receipt decides between uncertain and not_executed, and a not_executed
// view never carries the three process statistics.
func TestAppKillEmptyProcessSetRemovalOutcome(t *testing.T) {
	newEmpty := func(t *testing.T) (*killHarness, *protocol.Observation) {
		t.Helper()
		h := newKillHarness(t)
		first := h.startSession()
		task := device.Task{TaskID: 201, RootID: 201, DisplayID: 12, Package: "com.example.app"}
		h.addTask(task)
		h.ownTask(task, TaskOwned, nil)
		h.device.mu.Lock()
		h.device.processes = device.PackageProcesses{UserID: 10123,
			Instances: []device.ProcessInstance{}}
		h.device.mu.Unlock()
		return h, first.Observation
	}

	t.Run("unknown removal", func(t *testing.T) {
		h, observation := newEmpty(t)
		unknown := device.DispatchUnknown
		h.device.mu.Lock()
		h.device.removeReceipt = &unknown
		h.device.mu.Unlock()
		p, request := killRequest(observation, "Pkillemptyunknown", 201)
		result, _ := h.engine.App(context.Background(), "agent", p, request)
		if result.Outcome != OutcomeUncertain || result.Error == nil ||
			result.Error.Code != protocol.CodeKillUnconfirmed {
			t.Fatalf("empty unknown removal result=%+v", result)
		}
		if result.Error.Message != "确认无运行进程，但无法确认任务窗口已移除" {
			t.Fatalf("empty unknown removal message=%q", result.Error.Message)
		}
		if !result.App.HasKilledPIDs || result.App.KilledPIDs == nil ||
			len(result.App.KilledPIDs) != 0 || !result.App.HasRemainingPIDs ||
			result.App.RemainingPIDs == nil || len(result.App.RemainingPIDs) != 0 ||
			result.App.Restarted == nil || *result.App.Restarted {
			t.Fatalf("empty unknown removal facts=%+v", result.App)
		}
		h.engine.mu.Lock()
		entry := h.engine.sessionTaskLocked(h.engine.session, 201)
		h.engine.mu.Unlock()
		if entry == nil {
			t.Fatalf("unconfirmed removal dropped the registration")
		}
	})

	t.Run("not dispatched removal", func(t *testing.T) {
		h, observation := newEmpty(t)
		notDispatched := device.DispatchNotDispatched
		h.device.mu.Lock()
		h.device.removeReceipt = &notDispatched
		h.device.mu.Unlock()
		p, request := killRequest(observation, "Pkillemptynotdispatched", 201)
		result, _ := h.engine.App(context.Background(), "agent", p, request)
		if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
			result.Error.Code != protocol.CodeStopped {
			t.Fatalf("empty not dispatched removal result=%+v", result)
		}
		if result.App.HasKilledPIDs || result.App.KilledPIDs != nil ||
			result.App.HasRemainingPIDs || result.App.RemainingPIDs != nil ||
			result.App.HasRestarted || result.App.Restarted != nil {
			t.Fatalf("not_executed kill leaked process statistics: %+v", result.App)
		}
	})

	t.Run("revoked before removal", func(t *testing.T) {
		h, observation := newEmpty(t)
		h.script.onProcessRead = func(call int) {
			if call != 2 {
				return
			}
			h.engine.mu.Lock()
			h.engine.revokeSessionLocked("user_stop", time.Now())
			h.engine.mu.Unlock()
		}
		p, request := killRequest(observation, "Pkillemptyrevoked", 201)
		result, _ := h.engine.App(context.Background(), "agent", p, request)
		if result.Outcome != OutcomeNotExecuted || result.Error == nil ||
			result.Error.Code != protocol.CodeStopped {
			t.Fatalf("revoked removal result=%+v", result)
		}
		if result.App.HasKilledPIDs || result.App.KilledPIDs != nil ||
			result.App.HasRemainingPIDs || result.App.RemainingPIDs != nil ||
			result.App.HasRestarted || result.App.Restarted != nil {
			t.Fatalf("revoked kill leaked process statistics: %+v", result.App)
		}
		h.device.mu.Lock()
		removes := len(h.device.removeCalls)
		h.device.mu.Unlock()
		if removes != 0 {
			t.Fatalf("revoked kill dispatched %d removals", removes)
		}
	})
}

func TestAppKillTerminatesExactInstances(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"}
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.other", TaskID: intPtr(42)}
	h.device.processes = device.PackageProcesses{UserID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
		{PID: 501, StartTicks: 12, Name: "com.example.app:remote"},
	}}
	h.device.mu.Unlock()
	result, apiErr := h.engine.App(context.Background(), "agent", "Pkill",
		&protocol.AppRequest{Kind: protocol.AppKindKill, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 77, UseTask: true}})
	if apiErr != nil {
		t.Fatalf("app kill: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || !result.App.HasTargetPIDs ||
		len(result.App.TargetPIDs) != 2 || result.App.TargetPIDs[0] != 500 ||
		len(result.App.KilledPIDs) != 2 || !result.App.HasRemainingPIDs ||
		len(result.App.RemainingPIDs) != 0 || result.App.Restarted == nil ||
		*result.App.Restarted {
		t.Fatalf("kill result=%+v", result.App)
	}
	if len(h.device.killCalls) != 2 || h.device.killCalls[0] != 500 || h.device.killCalls[1] != 501 {
		t.Fatalf("kill calls=%v", h.device.killCalls)
	}
	h.device.mu.Lock()
	removes := append([]int(nil), h.device.removeCalls...)
	live := append([]device.Task(nil), h.device.tasks...)
	h.device.mu.Unlock()
	if len(removes) != 1 || removes[0] != 77 {
		t.Fatalf("remove calls=%v want [77]", removes)
	}
	for _, surviving := range live {
		if surviving.TaskID == 77 {
			t.Fatalf("task shell survived the kill: %+v", live)
		}
	}
	h.engine.mu.Lock()
	entry := h.engine.sessionTaskLocked(h.engine.session, 77)
	h.engine.mu.Unlock()
	if entry != nil {
		t.Fatalf("killed task stayed registered: %+v", entry)
	}
}

func TestAppKillConfirmedEmptyProcessSetIsExecutedWithoutSignal(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"}
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.other", TaskID: intPtr(42)}
	h.device.processes = device.PackageProcesses{UserID: 10123, Instances: []device.ProcessInstance{}}
	h.device.mu.Unlock()
	result, _ := h.engine.App(context.Background(), "agent", "Pkill",
		&protocol.AppRequest{Kind: protocol.AppKindKill, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 77, UseTask: true}})
	if result.Outcome != OutcomeExecuted || !result.App.HasTargetPIDs ||
		result.App.TargetPIDs == nil || len(result.App.TargetPIDs) != 0 ||
		result.App.KilledPIDs == nil || len(result.App.KilledPIDs) != 0 ||
		result.App.RemainingPIDs == nil || result.App.Restarted == nil || *result.App.Restarted {
		t.Fatalf("empty process set result=%+v", result.App)
	}
	if len(h.device.killCalls) != 0 {
		t.Fatalf("empty process set dispatched %d signals", len(h.device.killCalls))
	}
	h.device.mu.Lock()
	removes := append([]int(nil), h.device.removeCalls...)
	live := append([]device.Task(nil), h.device.tasks...)
	h.device.mu.Unlock()
	if len(removes) != 1 || removes[0] != 77 {
		t.Fatalf("remove calls=%v want [77]", removes)
	}
	for _, surviving := range live {
		if surviving.TaskID == 77 {
			t.Fatalf("task shell survived the kill: %+v", live)
		}
	}
	h.engine.mu.Lock()
	entry := h.engine.sessionTaskLocked(h.engine.session, 77)
	h.engine.mu.Unlock()
	if entry != nil {
		t.Fatalf("killed task stayed registered: %+v", entry)
	}
}

func TestAppKillUnconfirmedKeepsKnownPIDs(t *testing.T) {
	h := newHarness(t)
	first := h.startSession()
	task := device.Task{TaskID: 77, RootID: 77, DisplayID: 12, Package: "com.example.app"}
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	unknown := device.DispatchUnknown
	h.device.mu.Lock()
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.other", TaskID: intPtr(42)}
	h.device.processes = device.PackageProcesses{UserID: 10123, Instances: []device.ProcessInstance{
		{PID: 500, StartTicks: 11, Name: "com.example.app"},
	}}
	h.device.killReceipt = &unknown
	h.device.mu.Unlock()
	result, _ := h.engine.App(context.Background(), "agent", "Pkill",
		&protocol.AppRequest{Kind: protocol.AppKindKill, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 77, UseTask: true}})
	if result.Outcome != OutcomeUncertain || result.Error == nil ||
		result.Error.Code != protocol.CodeKillUnconfirmed {
		t.Fatalf("unconfirmed kill result=%+v", result)
	}
	if !result.App.HasKilledPIDs || result.App.KilledPIDs == nil ||
		!result.App.HasRemainingPIDs || result.App.RemainingPIDs[0] != 500 {
		t.Fatalf("kill facts=%+v", result.App)
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

// killScriptDevice models the Android task/foreground linkage the kill flow
// depends on: the scripted foreground is reported only while its task is still
// live on the queried display, and the death of the last process may take the
// task shell with it. Every other fact comes from the shared fake.
type killScriptDevice struct {
	*fakeDevice
	// dropTaskWhenProcessGone is a task id Android destroys once its package has
	// no live process left. Zero disables the behavior.
	dropTaskWhenProcessGone int
	// onProcessRead runs after one package process read with its 1-based call
	// number, outside the device lock, so a test can interleave a STOP.
	onProcessRead func(call int)
	processReads  int
}

// Foreground reports the scripted foreground while its task is still live.
func (d *killScriptDevice) Foreground(_ context.Context, displayID int) (device.Foreground, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.foregroundOfLocked(displayID), nil
}

// foregroundOfLocked resolves the scripted foreground against the live tasks.
// The caller holds the device lock.
func (d *killScriptDevice) foregroundOfLocked(displayID int) device.Foreground {
	if d.foreground == nil {
		return device.Foreground{State: device.ForegroundPresent, Package: "com.example", TaskID: intPtr(42)}
	}
	if d.foreground.TaskID != nil {
		for _, task := range d.tasks {
			if task.TaskID == *d.foreground.TaskID && task.DisplayID == displayID {
				return *d.foreground
			}
		}
		return device.Foreground{State: device.ForegroundNone}
	}
	return *d.foreground
}

// Capture attaches the same derived foreground to the captured page.
func (d *killScriptDevice) Capture(ctx context.Context, display device.Display) (device.Capture, error) {
	capture, err := d.fakeDevice.Capture(ctx, display)
	d.mu.Lock()
	capture.Foreground = d.foregroundOfLocked(display.ID)
	d.mu.Unlock()
	return capture, err
}

// PackageProcesses records the read and runs the optional interleaving hook.
func (d *killScriptDevice) PackageProcesses(ctx context.Context, pkg string) (device.PackageProcesses, error) {
	processes, err := d.fakeDevice.PackageProcesses(ctx, pkg)
	d.mu.Lock()
	d.processReads++
	call := d.processReads
	d.mu.Unlock()
	if d.onProcessRead != nil {
		d.onProcessRead(call)
	}
	return processes, err
}

// KillProcess drops the modeled task shell once the package has no process left.
func (d *killScriptDevice) KillProcess(ctx context.Context, pid int, startTicks uint64) (device.DispatchReceipt, error) {
	receipt, err := d.fakeDevice.KillProcess(ctx, pid, startTicks)
	if receipt == device.DispatchTerminated && err == nil {
		d.mu.Lock()
		if d.dropTaskWhenProcessGone != 0 && len(d.processes.Instances) == 0 {
			kept := d.tasks[:0]
			for _, task := range d.tasks {
				if task.TaskID == d.dropTaskWhenProcessGone {
					continue
				}
				kept = append(kept, task)
			}
			d.tasks = kept
		}
		d.mu.Unlock()
	}
	return receipt, err
}

// killHarness is the shared app harness plus the kill-specific device linkage.
type killHarness struct {
	*harness
	script *killScriptDevice
}

// newKillHarness builds the standard harness over the kill-specific device. It
// mirrors newHarness; the shared harness helper only accepts its own fake.
func newKillHarness(t *testing.T) *killHarness {
	t.Helper()
	base := newFakeDevice()
	script := &killScriptDevice{fakeDevice: base}
	cfg := DefaultConfig("instance-test", "agent")
	cfg.CleanupTimeout = 700 * time.Millisecond
	cfg.LedgerTimeout = 200 * time.Millisecond
	store := &fakeStore{}
	eng := New(cfg, nil, script, store,
		&testProjector{builder: observation.NewBuilder()}, testDiffer{})
	eng.SetDeviceReady(true)
	eng.StartClock()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		eng.Shutdown(ctx)
	})
	h := &harness{t: t, engine: eng, device: base, store: store,
		appInstance: "11111111-1111-4111-8111-111111111111",
		modeGen:     "22222222-2222-4222-8222-222222222222"}
	h.sync(nil, nil)
	return &killHarness{harness: h, script: script}
}

// TestAppKillForegroundVirtualTaskRemovesShell is the Issue #35 reproduction: the
// single foreground owned task of a virtual display must be terminable directly,
// and its dead task shell must not survive the kill.
func TestAppKillForegroundVirtualTaskRemovesShell(t *testing.T) {
	h := newKillHarness(t)
	first := h.startSession()
	task := device.Task{TaskID: 201, RootID: 201, DisplayID: 12, Package: "com.netease.cloudmusic"}
	h.addTask(task)
	h.ownTask(task, TaskOwned, nil)
	h.device.mu.Lock()
	h.device.foreground = &device.Foreground{State: device.ForegroundPresent,
		Package: "com.netease.cloudmusic", TaskID: intPtr(201)}
	h.device.processes = device.PackageProcesses{UserID: 10123, Instances: []device.ProcessInstance{
		{PID: 731, StartTicks: 55, Name: "com.netease.cloudmusic"},
	}}
	h.device.mu.Unlock()

	result, apiErr := h.engine.App(context.Background(), "agent", "Pkillfg",
		&protocol.AppRequest{Kind: protocol.AppKindKill, Reference: first.Observation.ID,
			Target: &protocol.AppTarget{TaskID: 201, UseTask: true}})
	if apiErr != nil {
		t.Fatalf("app kill: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || result.Error != nil {
		t.Fatalf("foreground owned kill outcome=%q error=%+v", result.Outcome, result.Error)
	}
	h.device.mu.Lock()
	kills := append([]int(nil), h.device.killCalls...)
	removes := append([]int(nil), h.device.removeCalls...)
	live := append([]device.Task(nil), h.device.tasks...)
	h.device.mu.Unlock()
	if len(kills) != 1 || kills[0] != 731 {
		t.Fatalf("kill calls=%v want [731]", kills)
	}
	if len(removes) != 1 || removes[0] != 201 {
		t.Fatalf("remove calls=%v want [201]", removes)
	}
	for _, surviving := range live {
		if surviving.TaskID == 201 {
			t.Fatalf("task shell survived the kill: %+v", live)
		}
	}
	h.engine.mu.Lock()
	entry := h.engine.sessionTaskLocked(h.engine.session, 201)
	h.engine.mu.Unlock()
	if entry != nil {
		t.Fatalf("killed task stayed registered: %+v", entry)
	}
	if result.Observation == nil || result.Observation.Foreground == nil {
		t.Fatalf("post observation has no foreground: %+v", result.Observation)
	}
	if result.Observation.Foreground.State != device.ForegroundNone ||
		result.Observation.Foreground.TaskID != nil {
		t.Fatalf("post observation foreground=%+v want an absent foreground", result.Observation.Foreground)
	}
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
