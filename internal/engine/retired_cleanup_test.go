package engine

import (
	"context"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Regressions for issue #2. The original red fixture remains in maintenance.
// Fake device facts do not establish the original phone-side trigger.
func TestIssue2RecentOnlyOwnedTaskIsRemoved(t *testing.T) {
	h := newHarnessWithClock(t, false)
	h.device.mu.Lock()
	h.device.recentKnown = true
	h.device.recentIDs = map[int]bool{41: true}
	h.device.mu.Unlock()
	tasks, recents, checked := h.engine.removeOwnedTasks(context.Background(), 12,
		[]taskEntry{{TaskID: 41, DisplayID: 12, Proven: true}})
	h.device.mu.Lock()
	calls := append([]int(nil), h.device.removeCalls...)
	h.device.mu.Unlock()
	t.Logf("tasks=%t recents=%t checked=%v remove_calls=%v", tasks, recents, checked, calls)
	if !tasks || !recents || len(calls) != 1 || calls[0] != 41 {
		t.Fatal("recent-only owned task was checked but never submitted for removal")
	}
}

func TestIssue2LiveCleanupRechecksAfterTransientRecentsReadFailure(t *testing.T) {
	h := newHarnessWithClock(t, false)
	start := h.startSession()
	h.engine.mu.Lock()
	h.engine.session.Tasks = []taskEntry{{TaskID: 41, DisplayID: 12, Proven: true}}
	h.engine.mu.Unlock()
	h.engine.dev = &transientRecentsDevice{fakeDevice: h.device}
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.mu.Unlock()
	ended, err := h.engine.End("agent", "", start.OperationID)
	if err != nil || ended.Outcome != OutcomeExecuted {
		t.Fatal("end did not revoke")
	}
	h.engine.mu.Lock()
	done := h.engine.cleanup.done
	h.engine.mu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bounded cleanup did not finish")
	}
	status, _ := h.engine.Status("agent", "")
	if status.Cleanup == nil || status.Cleanup.State != protocol.CleanupComplete || status.Cleanup.Recents != protocol.CleanupConfirmed || !*status.CanStart {
		t.Fatalf("transient recents failure did not converge: %+v", status)
	}
}

type transientRecentsDevice struct {
	*fakeDevice
	calls int
}

func (d *transientRecentsDevice) RecentTaskIDs(context.Context) (map[int]bool, bool) {
	d.calls++
	return map[int]bool{}, d.calls > 2
}

func TestIssue2RestartRechecksStaleUnconfirmedRecents(t *testing.T) {
	h := newHarnessWithClock(t, false)
	record := uiRecoveryTestLedger(2, uiRecoveryTestUI("terminated"))
	record.Session.State = SessionEnded
	record.Session.AppInstanceID = h.appInstance
	record.Session.ModeGeneration = h.modeGen
	record.Cleanup.Execution = protocol.CleanupConfirmed
	record.Cleanup.Recents = protocol.CleanupUnconfirmed
	record.Cleanup.CheckedTaskIDs = []int{41}
	h.device.mu.Lock()
	h.device.displayExists = false
	h.device.recentKnown = true
	h.device.recentIDs = map[int]bool{}
	h.device.mu.Unlock()
	h.engine.mu.Lock()
	h.engine.recovery = record
	h.engine.recovering = true
	h.engine.restoreRecoveryLocked(record)
	h.engine.mu.Unlock()
	for n := 0; n < 3; n++ {
		h.engine.RetryRecovery(context.Background())
	}
	status, _ := h.engine.Status("agent", "")
	t.Logf("can_start=%t blocking_code=%s recents=%s",
		*status.CanStart, status.BlockingCode, status.Cleanup.Recents)
	if !*status.CanStart {
		t.Fatal("startup recovery treats stale unconfirmed recents as an immutable fact")
	}
}
