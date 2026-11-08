package engine

import (
	"strconv"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The message store is bounded service policy: it neither persists to the
// ledger nor grows without limit across repeated calls.
const (
	maxStoredMessages     = 256
	maxStoredMessageBytes = 1 << 20
)

// messageState is one saved Agent message. Its body stays in memory only and
// must never reach a log, error or ledger.
type messageState struct {
	ID   string
	Text string
}

// Notify saves one Agent message for the control App message center. M4 has no
// delivery channel into the App, so notification stays explicitly unavailable
// without turning a saved message into a submitted or pending claim.
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
	messageID, count, totalBytes := e.storeMessage(req.Text)
	saved := true
	result := &protocol.Result{
		OperationID: op.ID,
		Outcome:     OutcomeExecuted,
		MessageID:   messageID,
		Saved:       &saved,
		Notification: &protocol.NotificationView{
			State: "unavailable",
			Error: &protocol.Error{
				Code:    protocol.CodeNotificationUnavailable,
				Message: "消息已保存，控制 App 消息中心尚未接入",
			},
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

// storeMessage appends one message and evicts the oldest entries until both the
// count and byte bounds hold. It returns the assigned id and the bounded store
// facts.
func (e *Engine) storeMessage(text string) (string, int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextMessage++
	id := "M" + strconv.FormatInt(e.nextMessage, 10)
	e.messages = append(e.messages, messageState{ID: id, Text: text})
	e.messageBytes += len(text)
	for len(e.messages) > maxStoredMessages || e.messageBytes > maxStoredMessageBytes {
		e.messageBytes -= len(e.messages[0].Text)
		copy(e.messages, e.messages[1:])
		e.messages[len(e.messages)-1] = messageState{}
		e.messages = e.messages[:len(e.messages)-1]
	}
	return id, len(e.messages), e.messageBytes
}
