package engine

import "github.com/malyjacob/mobile-use-vnext/internal/protocol"

// ordinaryBarrierLocked is the common admission barrier for every ordinary
// tool. The caller holds e.mu. It deliberately does not inspect e.work: the
// existing work-position conflict check remains separate so a trusted apps
// cache can still be served while unrelated work is active.
func (e *Engine) ordinaryBarrierLocked(op *operationState) *protocol.APIError {
	if e.ledgerUnsafe {
		return protocol.NewErrorOutcome(503, protocol.CodeDeviceUnavailable,
			"恢复账本不可用，普通工作已暂停", OutcomeNotExecuted)
	}
	if e.uiPendingLocked() {
		owner := ""
		blocking := ""
		if e.ui != nil {
			blocking = e.ui.record.OperationID
		}
		if e.session != nil {
			owner = e.session.Agent
		}
		return ordinaryBusyError(op, owner, blocking, "原输入调用结束未确认")
	}
	if e.shell != nil && !e.shell.settled() {
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
