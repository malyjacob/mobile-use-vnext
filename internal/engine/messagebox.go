package engine

import (
	"context"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The message box is bounded internal policy behind notify. It is not part of
// the recovery ledger and holds no execution authority: a message store
// failure never changes STOP, cleanup or admission.
const (
	MessageFormat  = "mobile-use-vnext-messages"
	MessageVersion = 1

	maxStoredMessages     = 256
	maxStoredMessageBytes = 1 << 20

	// Delivery is bounded per sync so one heartbeat cannot carry the whole
	// queue. Unacknowledged items stay in order and are resent.
	messageDeliveryMaxItems = 8
	messageDeliveryMaxBytes = 32 << 10
	maxMessageAcks          = 32
	maxMessageIDLen         = 64
)

// messageState is one saved Agent message. Its body stays in memory and in the
// private message file only; it never reaches a log, error or ledger.
type messageState struct {
	ID        string
	Seq       int64
	Text      string
	CreatedAt time.Time
}

// StoredMessage is one durable message record in the private message file.
type StoredMessage struct {
	ID        string    `json:"id"`
	Seq       int64     `json:"seq"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// MessageBox is the complete private message file content. NextSeq makes ids
// unique across service restarts so the App can deduplicate redelivery.
type MessageBox struct {
	Format   string          `json:"format"`
	Version  int             `json:"version"`
	NextSeq  int64           `json:"next_seq"`
	Messages []StoredMessage `json:"messages"`
}

// MessageStore is the private bounded store injected by cmd. It persists only
// Agent messages, never recovery ownership.
type MessageStore interface {
	Load() (*MessageBox, error)
	Save(ctx context.Context, box *MessageBox) error
}

// WithMessageStore supplies the private message file behind notify. Without it
// messages remain runtime-only, which is never reported as durable delivery.
func WithMessageStore(store MessageStore) Option {
	return func(e *Engine) { e.messageStore = store }
}

// loadMessages reads the private message file during startup. A malformed file
// disables further writes so the unreadable evidence is not overwritten; the
// service keeps running and can still save new messages in memory only.
func (e *Engine) loadMessages() {
	if e.messageStore == nil {
		return
	}
	box, err := e.messageStore.Load()
	if err != nil {
		e.disableMessageStore("message_store_load_failed")
		e.seedMessageCounter()
		return
	}
	if box == nil {
		// A missing file can coexist with an App that still holds earlier
		// messages (for example after service state was recreated). Start the
		// id range from wall-clock milliseconds so a new message cannot reuse
		// an id the App already deduplicates.
		e.seedMessageCounter()
		return
	}
	messages, bytes, next, ok := validateMessageBox(box)
	if !ok {
		e.disableMessageStore("message_store_corrupt")
		e.seedMessageCounter()
		return
	}
	e.mu.Lock()
	e.messages = messages
	e.messageBytes = bytes
	if next > e.nextMessage {
		e.nextMessage = next
	}
	e.mu.Unlock()
}

func (e *Engine) disableMessageStore(code string) {
	e.mu.Lock()
	e.messageStore = nil
	e.mu.Unlock()
	if e.log != nil {
		e.log.Event(eventlog.Error, "message.store_disabled", eventlog.Str("code", code))
	}
}

// seedMessageCounter moves the id range beyond any plausible id issued by an
// earlier App-visible queue when no readable message file exists. Ids stay
// opaque bounded strings; only uniqueness for App deduplication matters.
func (e *Engine) seedMessageCounter() {
	seed := time.Now().UnixMilli()
	e.mu.Lock()
	if seed > e.nextMessage {
		e.nextMessage = seed
	}
	e.mu.Unlock()
}

// validateMessageBox enforces the same bounds used when writing, so a corrupted
// or hand-edited file cannot make delivery or memory unbounded.
func validateMessageBox(box *MessageBox) ([]messageState, int, int64, bool) {
	if box == nil || box.Format != MessageFormat || box.Version != MessageVersion {
		return nil, 0, 0, false
	}
	if box.NextSeq < 1 || len(box.Messages) > maxStoredMessages {
		return nil, 0, 0, false
	}
	seen := make(map[string]bool, len(box.Messages))
	total := 0
	next := box.NextSeq
	for _, stored := range box.Messages {
		if !validMessageID(stored.ID) || seen[stored.ID] || stored.Seq <= 0 || stored.Seq > box.NextSeq {
			return nil, 0, 0, false
		}
		if stored.CreatedAt.IsZero() || stored.Text == "" ||
			utf8.RuneCountInString(stored.Text) > protocol.MaxTextRunes {
			return nil, 0, 0, false
		}
		seen[stored.ID] = true
		total += len(stored.Text)
		if total > maxStoredMessageBytes {
			return nil, 0, 0, false
		}
		if stored.Seq > next {
			next = stored.Seq
		}
	}
	messages := make([]messageState, 0, len(box.Messages))
	for _, stored := range box.Messages {
		messages = append(messages, messageState{
			ID: stored.ID, Seq: stored.Seq, Text: stored.Text, CreatedAt: stored.CreatedAt,
		})
	}
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].Seq < messages[j].Seq })
	return messages, total, next, true
}

// storeMessage appends one message and evicts the oldest entries until both
// bounds hold. It returns the assigned identity and bounded store facts.
func (e *Engine) storeMessage(text string, now time.Time) (string, int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextMessage++
	message := messageState{
		ID: "M" + strconv.FormatInt(e.nextMessage, 10), Seq: e.nextMessage,
		Text: text, CreatedAt: now,
	}
	e.messages = append(e.messages, message)
	e.messageBytes += len(text)
	for len(e.messages) > maxStoredMessages || e.messageBytes > maxStoredMessageBytes {
		e.messageBytes -= len(e.messages[0].Text)
		copy(e.messages, e.messages[1:])
		e.messages[len(e.messages)-1] = messageState{}
		e.messages = e.messages[:len(e.messages)-1]
	}
	return message.ID, len(e.messages), e.messageBytes
}

// ackMessagesLocked releases exactly the acknowledged ids. Unknown or already
// released ids are ignored; malformed ids are rejected before this call.
func (e *Engine) ackMessagesLocked(ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	acked := make(map[string]bool, len(ids))
	for _, id := range ids {
		acked[id] = true
	}
	changed := false
	kept := e.messages[:0]
	for _, message := range e.messages {
		if acked[message.ID] {
			e.messageBytes -= len(message.Text)
			changed = true
			continue
		}
		kept = append(kept, message)
	}
	e.messages = kept
	return changed
}

// deliveryMessagesLocked returns the oldest unacknowledged messages within the
// per-sync item and byte budget. The service does not mark them delivered:
// only an App acknowledgement releases them.
func (e *Engine) deliveryMessagesLocked() []protocol.AppMessage {
	out := make([]protocol.AppMessage, 0, messageDeliveryMaxItems)
	bytes := 0
	for _, message := range e.messages {
		if len(out) >= messageDeliveryMaxItems {
			break
		}
		if bytes+len(message.Text) > messageDeliveryMaxBytes && len(out) > 0 {
			break
		}
		out = append(out, protocol.AppMessage{
			ID: message.ID, Seq: message.Seq, Text: message.Text,
			CreatedAt: formatTime(message.CreatedAt),
		})
		bytes += len(message.Text)
	}
	return out
}

// persistMessages snapshots the current queue while holding the state lock and
// writes it outside the state lock. Saves are serialised so an older snapshot
// can never replace a newer one.
func (e *Engine) persistMessages() {
	e.messagePersistMu.Lock()
	defer e.messagePersistMu.Unlock()
	e.mu.Lock()
	store := e.messageStore
	if store == nil {
		e.mu.Unlock()
		return
	}
	box := e.messageBoxLocked()
	e.mu.Unlock()
	if err := store.Save(context.Background(), box); err != nil {
		e.mu.Lock()
		first := !e.messageSaveFailing
		e.messageSaveFailing = true
		e.mu.Unlock()
		if first && e.log != nil {
			e.log.Event(eventlog.Error, "message.store_failed", eventlog.Str("code", "message_store_unavailable"))
		}
		return
	}
	e.mu.Lock()
	recovered := e.messageSaveFailing
	e.messageSaveFailing = false
	e.mu.Unlock()
	if recovered && e.log != nil {
		e.log.Event(eventlog.Info, "message.store_recovered", eventlog.Int("count", len(box.Messages)))
	}
}

func (e *Engine) messageBoxLocked() *MessageBox {
	messages := make([]StoredMessage, 0, len(e.messages))
	for _, message := range e.messages {
		messages = append(messages, StoredMessage{
			ID: message.ID, Seq: message.Seq, Text: message.Text, CreatedAt: message.CreatedAt,
		})
	}
	return &MessageBox{Format: MessageFormat, Version: MessageVersion,
		NextSeq: e.nextMessage, Messages: messages}
}

// validMessageID accepts the canonical M<positive integer> id shape. A body is
// never accepted as an id, and ids are bounded like every other reference.
func validMessageID(id string) bool {
	if len(id) < 2 || len(id) > maxMessageIDLen || id[0] != 'M' {
		return false
	}
	if id[1] < '1' || id[1] > '9' {
		return false
	}
	for i := 2; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	return true
}
