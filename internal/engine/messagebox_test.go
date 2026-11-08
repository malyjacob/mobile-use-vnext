package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type fakeMessageStore struct {
	mu      sync.Mutex
	box     *MessageBox
	loadErr error
	saveErr error
	saves   int
}

func (s *fakeMessageStore) Load() (*MessageBox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.box == nil {
		return nil, nil
	}
	copied := *s.box
	copied.Messages = append([]StoredMessage(nil), s.box.Messages...)
	return &copied, nil
}

func (s *fakeMessageStore) Save(ctx context.Context, box *MessageBox) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	copied := *box
	copied.Messages = append([]StoredMessage(nil), box.Messages...)
	s.box = &copied
	s.saves++
	return nil
}

func newMessageEngine(t *testing.T, store MessageStore) *Engine {
	t.Helper()
	cfg := DefaultConfig("instance-test", "agent")
	options := []Option{}
	if store != nil {
		options = append(options, WithMessageStore(store))
	}
	eng := New(cfg, nil, newFakeDevice(), &fakeStore{},
		&testProjector{builder: observation.NewBuilder()}, testDiffer{}, options...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		eng.Shutdown(ctx)
	})
	return eng
}

func syncWithAcks(t *testing.T, eng *Engine, acks []string) *protocol.AppSyncResponse {
	t.Helper()
	req := readySync()
	req.MessageAcks = acks
	response, apiErr := eng.SyncApp(context.Background(), req)
	if apiErr != nil {
		t.Fatalf("sync rejected: %v", apiErr)
	}
	if response.Messages == nil {
		t.Fatalf("messages must always be a JSON array, not null")
	}
	return response
}

func TestSyncDeliversSavedMessagesOldestFirst(t *testing.T) {
	eng := newMessageEngine(t, nil)
	for i := 0; i < 10; i++ {
		if _, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: "消息"}); apiErr != nil {
			t.Fatalf("notify %d rejected: %v", i, apiErr)
		}
	}
	response := syncWithAcks(t, eng, nil)
	if len(response.Messages) != messageDeliveryMaxItems {
		t.Fatalf("delivered %d messages, want %d", len(response.Messages), messageDeliveryMaxItems)
	}
	if response.Messages[0].ID != "M1" || response.Messages[0].Seq != 1 ||
		response.Messages[len(response.Messages)-1].ID != "M8" {
		t.Fatalf("unexpected delivery order: %+v", response.Messages)
	}
	if response.Messages[0].CreatedAt == "" || response.Messages[0].Text != "消息" {
		t.Fatalf("message projection: %+v", response.Messages[0])
	}
	// Delivery is not acknowledgement: the same unconfirmed items are resent.
	again := syncWithAcks(t, eng, nil)
	if len(again.Messages) != messageDeliveryMaxItems || again.Messages[0].ID != "M1" {
		t.Fatalf("unacknowledged messages must be resent: %+v", again.Messages)
	}
}

func TestSyncAcksReleaseMessagesAndIgnoreUnknown(t *testing.T) {
	eng := newMessageEngine(t, nil)
	for i := 0; i < 3; i++ {
		if _, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: "消息"}); apiErr != nil {
			t.Fatalf("notify %d rejected: %v", i, apiErr)
		}
	}
	response := syncWithAcks(t, eng, []string{"M1", "M99"})
	if len(response.Messages) != 2 || response.Messages[0].ID != "M2" ||
		response.Messages[1].ID != "M3" {
		t.Fatalf("ack processing must release before delivery projection: %+v", response.Messages)
	}
	after := syncWithAcks(t, eng, nil)
	if len(after.Messages) != 2 || after.Messages[0].ID != "M2" || after.Messages[1].ID != "M3" {
		t.Fatalf("ack did not release exactly the acknowledged message: %+v", after.Messages)
	}
	// Re-acknowledging a released id is harmless.
	after = syncWithAcks(t, eng, []string{"M1", "M2"})
	last := syncWithAcks(t, eng, nil)
	if len(last.Messages) != 1 || last.Messages[0].ID != "M3" {
		t.Fatalf("released messages must not return: %+v", last.Messages)
	}
}

func TestSyncRejectsMalformedMessageAcks(t *testing.T) {
	eng := newMessageEngine(t, nil)
	requests := []*protocol.AppSyncRequest{}
	invalid := readySync()
	invalid.MessageAcks = []string{"not-a-message"}
	requests = append(requests, invalid)
	trailingZero := readySync()
	trailingZero.MessageAcks = []string{"M01"}
	requests = append(requests, trailingZero)
	tooMany := readySync()
	for i := 0; i < maxMessageAcks+1; i++ {
		tooMany.MessageAcks = append(tooMany.MessageAcks, "M1")
	}
	requests = append(requests, tooMany)
	for i, req := range requests {
		if _, apiErr := eng.SyncApp(context.Background(), req); apiErr == nil || apiErr.Code != protocol.CodeInvalidRequest {
			t.Fatalf("request %d: malformed message_acks were accepted: %+v", i, apiErr)
		}
	}
}

func TestSyncDeliveryStaysWithinByteBudget(t *testing.T) {
	eng := newMessageEngine(t, nil)
	large := strings.Repeat("字", protocol.MaxTextRunes)
	for i := 0; i < 4; i++ {
		if _, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: large}); apiErr != nil {
			t.Fatalf("large notify %d rejected: %v", i, apiErr)
		}
	}
	response := syncWithAcks(t, eng, nil)
	total := 0
	for _, message := range response.Messages {
		total += len(message.Text)
	}
	if total > messageDeliveryMaxBytes || len(response.Messages) != 2 {
		t.Fatalf("delivery budget: items=%d bytes=%d", len(response.Messages), total)
	}
}

func TestMessagesSurviveRestartAndPersistAcks(t *testing.T) {
	store := &fakeMessageStore{}
	first := newMessageEngine(t, store)
	for i := 0; i < 2; i++ {
		if _, apiErr := first.Notify("agent", "", &protocol.NotifyRequest{Text: "消息"}); apiErr != nil {
			t.Fatalf("notify %d rejected: %v", i, apiErr)
		}
	}
	syncWithAcks(t, first, []string{"M1"})

	second := newMessageEngine(t, store)
	second.loadMessages()
	response := syncWithAcks(t, second, nil)
	if len(response.Messages) != 1 || response.Messages[0].ID != "M2" {
		t.Fatalf("restart must keep only unacknowledged messages: %+v", response.Messages)
	}
	// The next message gets a fresh id even after a restart.
	if _, apiErr := second.Notify("agent", "", &protocol.NotifyRequest{Text: "新消息"}); apiErr != nil {
		t.Fatalf("post-restart notify rejected: %v", apiErr)
	}
	third := newMessageEngine(t, store)
	third.loadMessages()
	response = syncWithAcks(t, third, nil)
	if len(response.Messages) != 2 || response.Messages[0].ID != "M2" ||
		response.Messages[1].ID != "M3" {
		t.Fatalf("persisted ids must stay monotonic: %+v", response.Messages)
	}
}

func TestMessageStoreFailureKeepsRuntimeDelivery(t *testing.T) {
	store := &fakeMessageStore{loadErr: errors.New("unreadable")}
	eng := newMessageEngine(t, store)
	eng.loadMessages()
	if eng.messageStore != nil {
		t.Fatalf("an unreadable message file must disable overwriting it")
	}
	first, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: "消息"})
	if apiErr != nil {
		t.Fatalf("notify must still work in memory: %v", apiErr)
	}
	// The App may still hold ids from the unreadable queue; a fresh id must not
	// collide with them.
	if first.MessageID == "M1" || len(first.MessageID) < 8 {
		t.Fatalf("message id after store loss was not reseeded: %q", first.MessageID)
	}
	if response := syncWithAcks(t, eng, nil); len(response.Messages) != 1 {
		t.Fatalf("runtime delivery after store failure: %+v", response.Messages)
	}

	missing := newMessageEngine(t, &fakeMessageStore{})
	missing.loadMessages()
	first, apiErr = missing.Notify("agent", "", &protocol.NotifyRequest{Text: "消息"})
	if apiErr != nil {
		t.Fatalf("notify with a missing message file: %v", apiErr)
	}
	if first.MessageID == "M1" || len(first.MessageID) < 8 {
		t.Fatalf("message id with a missing file was not reseeded: %q", first.MessageID)
	}

	saveFailing := &fakeMessageStore{saveErr: errors.New("disk full")}
	failing := newMessageEngine(t, saveFailing)
	if _, apiErr := failing.Notify("agent", "", &protocol.NotifyRequest{Text: "消息"}); apiErr != nil {
		t.Fatalf("notify must not fail on a message store error: %v", apiErr)
	}
	if response := syncWithAcks(t, failing, nil); len(response.Messages) != 1 {
		t.Fatalf("delivery must not depend on the message file: %+v", response.Messages)
	}
}

func TestMessageTextNeverReachesLogs(t *testing.T) {
	out := &lockedBuffer{}
	logger := eventlog.New(out, "instance-test")
	eng := New(DefaultConfig("instance-test", "agent"), logger, newFakeDevice(),
		&fakeStore{}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		eng.Shutdown(ctx)
	})
	secret := "敏感消息-只允许出现在同步响应"
	if _, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: secret}); apiErr != nil {
		t.Fatalf("notify rejected: %v", apiErr)
	}
	response := syncWithAcks(t, eng, nil)
	if len(response.Messages) != 1 || response.Messages[0].Text != secret {
		t.Fatalf("sync delivery lost the message body")
	}
	syncWithAcks(t, eng, []string{"M1"})
	logger.Close()
	logged := out.String()
	if strings.Contains(logged, secret) || strings.Contains(logged, "敏感消息") {
		t.Fatalf("log leaked the message text")
	}
	if !strings.Contains(logged, "notify.saved") {
		t.Fatalf("bounded notify event missing")
	}
}
