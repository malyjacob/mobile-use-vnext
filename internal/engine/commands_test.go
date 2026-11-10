package engine

import (
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestCreateCommandSingleDeliveryAndMatchingReceipt(t *testing.T) {
	h := newHarness(t)
	h.engine.mu.Lock()
	h.engine.session = &sessionState{Origin: "Pcreating", Agent: "agent", Mode: "virtual", State: SessionStarting, AppInstanceID: h.appInstance, CreateCommand: "create-1"}
	h.engine.commands = []*appCommand{{Command: protocol.AppCommand{ID: "create-1", Kind: "create_display"}, Kind: "create_display", AppInstance: h.appInstance, RequestedAt: time.Now(), Ready: true}}
	first := h.engine.pendingCommandsLocked(time.Now())
	second := h.engine.pendingCommandsLocked(time.Now())
	h.engine.acceptCommandResultsLocked(&protocol.AppSyncRequest{AppInstanceID: h.appInstance, CommandResults: []protocol.AppCommandResult{{CommandID: "wrong", OK: true}}})
	matchedWrong := h.engine.session.CreateResult != nil
	h.engine.acceptCommandResultsLocked(&protocol.AppSyncRequest{AppInstanceID: h.appInstance, CommandResults: []protocol.AppCommandResult{{CommandID: "create-1", OK: false}}})
	remaining := len(h.engine.commands)
	h.engine.session = nil // synthetic session has no physical resources
	h.engine.mu.Unlock()
	if len(first) != 1 || len(second) != 0 {
		t.Errorf("create deliveries: first=%d second=%d", len(first), len(second))
	}
	if matchedWrong {
		t.Error("wrong command ID changed create result")
	}
	if remaining != 0 {
		t.Error("negative receipt without error_code retained command")
	}
}

func TestCreateCommandRequiresCurrentStartingOwner(t *testing.T) {
	for _, state := range []string{SessionActive, SessionEnded, SessionStarting} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			h.engine.mu.Lock()
			h.engine.session = &sessionState{State: state, AppInstanceID: h.appInstance, CreateCommand: "current-create"}
			h.engine.commands = []*appCommand{{Command: protocol.AppCommand{ID: "old-create", Kind: "create_display"}, Kind: "create_display", AppInstance: h.appInstance, RequestedAt: time.Now(), Ready: true}}
			got := h.engine.pendingCommandsLocked(time.Now())
			h.engine.session = nil
			h.engine.mu.Unlock()
			if len(got) != 0 {
				t.Error("create delivered outside its own starting session")
			}
		})
	}
}

func TestDisplayCommandsDiscardOldInstanceAndTimeout(t *testing.T) {
	h := newHarness(t)
	h.engine.enqueueCloseCommand(12, testDisplayGen, h.appInstance)
	h.engine.mu.Lock()
	h.engine.onAppInstanceChangedLocked("44444444-4444-4444-8444-444444444444", time.Now())
	oldCount := len(h.engine.commands)
	h.engine.mu.Unlock()
	if oldCount != 0 {
		t.Error("old-instance command retained")
	}
	h.engine.enqueueCloseCommand(12, "timeout-gen", h.appInstance)
	h.engine.mu.Lock()
	for _, c := range h.engine.commands {
		c.RequestedAt = time.Now().Add(-time.Hour)
	}
	got := h.engine.pendingCommandsLocked(time.Now())
	remaining := len(h.engine.commands)
	h.engine.mu.Unlock()
	if len(got) != 0 || remaining != 0 {
		t.Error("timed-out display command still queued or delivered")
	}
}

// TestCloseCommandsDeliverPerGenerationAndRetryUsesNewID proves that
// closes for the same display with different generations are separate queue
// entries, are delivered one per sync in order, only an owner-instance receipt
// closes one, and an acknowledged target is never regenerated.
func TestCloseCommandsDeliverPerGenerationAndRetryUsesNewID(t *testing.T) {
	h := newHarness(t)
	h.engine.mu.Lock()
	h.engine.cfg.CleanupTimeout = time.Minute // deterministic delivery window
	h.engine.mu.Unlock()
	h.engine.enqueueCloseCommand(12, "generation-a", h.appInstance)
	h.engine.enqueueCloseCommand(12, "generation-b", h.appInstance)
	h.engine.enqueueCloseCommand(12, "generation-a", h.appInstance) // exact target duplicate
	deliver := func() []protocol.AppCommand {
		h.engine.mu.Lock()
		defer h.engine.mu.Unlock()
		return h.engine.pendingCommandsLocked(time.Now())
	}
	first := deliver()
	if len(first) != 1 || first[0].TargetGeneration != "generation-a" ||
		first[0].TargetAppInstanceID != h.appInstance ||
		first[0].TargetDisplayID == nil || *first[0].TargetDisplayID != 12 {
		t.Fatalf("first close delivery: %+v", first)
	}
	// A receipt from a different App instance must not acknowledge this command.
	h.engine.mu.Lock()
	h.engine.acceptCommandResultsLocked(&protocol.AppSyncRequest{
		AppInstanceID:  "44444444-4444-4444-8444-444444444444",
		CommandResults: []protocol.AppCommandResult{{CommandID: first[0].ID, OK: true}},
	})
	h.engine.mu.Unlock()
	// The next delivery is the other generation, never a repeat of gen-a.
	second := deliver()
	if len(second) != 1 || second[0].TargetGeneration != "generation-b" {
		t.Fatalf("second close delivery: %+v", second)
	}
	// The owner receipt closes only generation-a; generation-b stays queued.
	h.engine.mu.Lock()
	h.engine.acceptCommandResultsLocked(&protocol.AppSyncRequest{
		AppInstanceID:  h.appInstance,
		CommandResults: []protocol.AppCommandResult{{CommandID: first[0].ID, OK: true}},
	})
	remaining := len(h.engine.commands)
	h.engine.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("owner receipt closed the wrong number of commands: %d", remaining)
	}
	if got := deliver(); len(got) != 0 {
		t.Errorf("delivered generation-b was redelivered before its receipt: %+v", got)
	}
	h.engine.mu.Lock()
	h.engine.acceptCommandResultsLocked(&protocol.AppSyncRequest{
		AppInstanceID: h.appInstance,
		CommandResults: []protocol.AppCommandResult{{CommandID: second[0].ID, OK: false,
			ErrorCode: stringPtr("close_failed")}},
	})
	queued := len(h.engine.commands)
	h.engine.mu.Unlock()
	if queued != 0 {
		t.Fatalf("negative receipt left %d commands queued", queued)
	}
	if got := deliver(); len(got) != 0 {
		t.Errorf("acknowledged commands were redelivered: %+v", got)
	}
	h.engine.enqueueCloseCommand(12, "generation-a", h.appInstance)
	h.engine.mu.Lock()
	regenerated := len(h.engine.commands)
	h.engine.mu.Unlock()
	if regenerated != 1 {
		t.Fatalf("next bounded round did not enqueue: %d", regenerated)
	}
	h.engine.mu.Lock()
	retryID := h.engine.commands[0].Command.ID
	h.engine.mu.Unlock()
	if retryID == first[0].ID {
		t.Fatal("retry reused an acknowledged command ID")
	}
}

func stringPtr(v string) *string { return &v }

func TestCloseCommandDeduplicatesPendingTargetAndAllowsNextRound(t *testing.T) {
	h := newHarness(t)
	h.engine.enqueueCloseCommand(12, "generation-a", h.appInstance)
	h.engine.enqueueCloseCommand(12, "generation-b", h.appInstance)
	h.engine.mu.Lock()
	count := len(h.engine.commands)
	var id string
	if count > 0 {
		id = h.engine.commands[0].Command.ID
	}
	h.engine.acceptCommandResultsLocked(&protocol.AppSyncRequest{AppInstanceID: h.appInstance, CommandResults: []protocol.AppCommandResult{{CommandID: id, OK: true}}})
	h.engine.mu.Unlock()
	if count != 2 {
		t.Errorf("different generations collapsed: %d", count)
	}
	h.engine.enqueueCloseCommand(12, "generation-a", h.appInstance)
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	retries := 0
	for _, c := range h.engine.commands {
		if c.Command.TargetGeneration == "generation-a" {
			retries++
			if c.Command.ID == id {
				t.Error("retry reused acknowledged ID")
			}
		}
	}
	if retries != 1 {
		t.Fatalf("next-round target count=%d", retries)
	}
}
