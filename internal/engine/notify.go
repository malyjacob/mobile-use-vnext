package engine

import (
	"time"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Notify saves one Agent message for the control App message center. The
// control sync is the delivery channel, so notification.state is pending
// (saved and queued) rather than a claim that the user saw it. executed still
// means only "the message was saved".
func (e *Engine) Notify(agent, p string, req *protocol.NotifyRequest) (*protocol.Result, *protocol.APIError) {
	op, duplicate, apiErr := e.registerDetachedOperation(p, agent, "notify")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		// The message is saved by the first accepted call only.
		return duplicate, nil
	}
	if req == nil || req.Text == "" {
		return e.finishWithError(op, "admission", protocol.Invalid("notify 的 text 不能为空")), nil
	}
	if utf8.RuneCountInString(req.Text) > protocol.MaxTextRunes {
		return e.finishWithError(op, "admission",
			protocol.Invalid("text 超过长度上限").WithDetail("limit", protocol.MaxTextRunes)), nil
	}
	messageID, count, totalBytes := e.storeMessage(req.Text, time.Now())
	e.persistMessages()
	saved := true
	result := &protocol.Result{
		OperationID: op.ID,
		Outcome:     OutcomeExecuted,
		MessageID:   messageID,
		Saved:       &saved,
		Notification: &protocol.NotificationView{
			State: "pending",
		},
	}
	e.finishOperation(op, OutcomeExecuted, "saved", nil)
	if e.log != nil {
		// Only bounded counts are logged; the text never is.
		e.log.Event(eventlog.Info, "notify.saved",
			eventlog.Int("count", count), eventlog.Int("bytes", totalBytes))
	}
	return result, nil
}
