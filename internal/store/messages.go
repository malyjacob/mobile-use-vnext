package store

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/malyjacob/mobile-use-vnext/internal/engine"
)

// MaxMessagesBytes bounds the private message file. The engine enforces the
// message count and body budget; this is the outer file guard.
const MaxMessagesBytes = 2 << 20

// Messages is the only implementation of the engine's private message store.
// It is a single-writer, atomically replaced file separate from the recovery
// ledger: message bodies never enter ownership evidence, logs or errors.
type Messages struct {
	mu   sync.Mutex
	path string
}

// NewMessages creates the message store rooted at dir.
func NewMessages(dir string) (*Messages, error) {
	if dir == "" {
		return nil, errors.New("message directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create message directory: %w", err)
	}
	return &Messages{path: filepath.Join(dir, "messages.json")}, nil
}

// Load reads the current private message file. A missing file returns (nil, nil).
func (m *Messages) Load() (*engine.MessageBox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, err := os.Stat(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat messages: %w", err)
	}
	if info.Size() > MaxMessagesBytes {
		return nil, errors.New("message file exceeds the size budget")
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		return nil, fmt.Errorf("read messages: %w", err)
	}
	var box engine.MessageBox
	if err := json.Unmarshal(data, &box, json.RejectUnknownMembers(true)); err != nil {
		return nil, errors.New("message file is unreadable")
	}
	if box.Format != engine.MessageFormat || box.Version != engine.MessageVersion {
		return nil, errors.New("message file format is unsupported")
	}
	return &box, nil
}

// Save durably writes the immutable message snapshot.
func (m *Messages) Save(ctx context.Context, box *engine.MessageBox) error {
	if box == nil {
		return errors.New("message box is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot := *box
	snapshot.Format = engine.MessageFormat
	snapshot.Version = engine.MessageVersion
	data, err := json.Marshal(&snapshot)
	if err != nil {
		return fmt.Errorf("encode messages: %w", err)
	}
	if len(data) > MaxMessagesBytes {
		return errors.New("message file exceeds the size budget")
	}
	if err := atomicReplace(m.path, data); err != nil {
		return err
	}
	return nil
}
