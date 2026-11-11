package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ordinaryBarrierLocked is the common admission barrier for every ordinary
// tool. The caller holds e.mu. A closed input window is never a barrier: the
// dispatch is over, and the only way to narrow an unknown end is to observe
// again. A managed shell keeps the barrier only until its bounded confirmation
// deadline, so an abandoned chain can never close admission indefinitely.
//
// It deliberately does not inspect e.work: the existing work-position conflict
// check remains separate so a trusted apps cache can still be served while
// unrelated work is active.
func (e *Engine) ordinaryBarrierLocked(op *operationState, now time.Time) *protocol.APIError {
	if e.ledgerUnsafe {
		return protocol.NewErrorOutcome(503, protocol.CodeDeviceUnavailable,
			"恢复账本不可用，普通工作已暂停", OutcomeNotExecuted)
	}
	if e.session != nil && e.session.State != SessionEnded && e.shellBarrierLocked(e.shell, now) {
		return ordinaryBusyError(op, e.shell.Agent, e.shell.OperationID,
			"设备工作尚未完成")
	}
	return nil
}

func ordinaryBusyError(op *operationState, owner, blocking, message string) *protocol.APIError {
	apiErr := protocol.NewErrorOutcome(409, protocol.CodeDeviceBusy,
		message, OutcomeNotExecuted)
	if op != nil && blocking != "" && owner == op.Agent {
		apiErr.WithDetail("blocking_operation_id", blocking)
	}
	return apiErr
}
