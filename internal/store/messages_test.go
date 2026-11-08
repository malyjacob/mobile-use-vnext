package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/engine"
)

func newMessageStore(t *testing.T) *Messages {
	t.Helper()
	store, err := NewMessages(t.TempDir())
	if err != nil {
		t.Fatalf("NewMessages: %v", err)
	}
	return store
}

func TestMessagesRoundTripAndRelease(t *testing.T) {
	store := newMessageStore(t)
	empty, err := store.Load()
	if err != nil || empty != nil {
		t.Fatalf("missing file: box=%v err=%v", empty, err)
	}
	box := &engine.MessageBox{
		Format: engine.MessageFormat, Version: engine.MessageVersion, NextSeq: 2,
		Messages: []engine.StoredMessage{
			{ID: "M1", Seq: 1, Text: "第一条", CreatedAt: time.Now()},
			{ID: "M2", Seq: 2, Text: "第二条", CreatedAt: time.Now()},
		},
	}
	if err := store.Save(context.Background(), box); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil || loaded.NextSeq != 2 || len(loaded.Messages) != 2 ||
		loaded.Messages[1].Text != "第二条" {
		t.Fatalf("round trip: %+v", loaded)
	}
	if loaded.Format != engine.MessageFormat || loaded.Version != engine.MessageVersion {
		t.Fatalf("format was not written canonically: %+v", loaded)
	}
	// Saving an acknowledged (released) queue removes the body from disk.
	if err := store.Save(context.Background(), &engine.MessageBox{NextSeq: 2,
		Messages: []engine.StoredMessage{{ID: "M2", Seq: 2, Text: "第二条", CreatedAt: time.Now()}}}); err != nil {
		t.Fatalf("Save after ack: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(store.path), "messages.json"))
	if err != nil {
		t.Fatalf("read message file: %v", err)
	}
	if strings.Contains(string(data), "第一条") {
		t.Fatal("acknowledged message body remained on disk")
	}
}

func TestMessagesRejectUnreadableOrOversizedFiles(t *testing.T) {
	store := newMessageStore(t)
	if err := os.WriteFile(store.path, []byte(`{"format":"mobile-use-vnext-messages","version":1,"next_seq":1,"messages":[],"unknown":1}`), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("unknown member was accepted")
	}
	if err := os.WriteFile(store.path, []byte(`{"format":"other","version":1,"next_seq":1,"messages":[]}`), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("foreign format was accepted")
	}
	if err := os.WriteFile(store.path, make([]byte, MaxMessagesBytes+1), 0o600); err != nil {
		t.Fatalf("write oversized file: %v", err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("oversized message file was accepted")
	}
}

func TestMessagesSaveRejectsOversizedSnapshot(t *testing.T) {
	store := newMessageStore(t)
	box := &engine.MessageBox{NextSeq: 1, Messages: []engine.StoredMessage{
		{ID: "M1", Seq: 1, Text: strings.Repeat("x", MaxMessagesBytes), CreatedAt: time.Now()},
	}}
	if err := store.Save(context.Background(), box); err == nil {
		t.Fatal("oversized snapshot was written")
	}
}
