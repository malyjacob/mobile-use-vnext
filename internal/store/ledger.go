// Package store implements the private recovery ledger as a single-writer,
// atomically replaced file. Durability follows the phone Debian chroot target:
// temp file -> fsync -> rename over the target -> fsync parent directory.
package store

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/malyjacob/mobile-use-vnext/internal/engine"
)

// MaxLedgerBytes bounds the single recovery object.
const MaxLedgerBytes = 64 * 1024

// Ledger is the only implementation of the engine's recovery store. One
// mutex serialises writers; a saved record carries a monotonic revision and
// the caller does not receive success before the durable rename completes.
type Ledger struct {
	mu       sync.Mutex
	path     string
	revision int64
	loaded   bool
}

// New creates the ledger store rooted at dir. The directory must already exist
// or be creatable by the caller.
func New(dir string) (*Ledger, error) {
	if dir == "" {
		return nil, errors.New("ledger directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create ledger directory: %w", err)
	}
	return &Ledger{path: filepath.Join(dir, "recovery-ledger.json")}, nil
}

// Load reads the current durable record. A missing file returns (nil, nil).
func (l *Ledger) Load() (*engine.Ledger, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	record, err := l.readLocked()
	if err != nil {
		return nil, err
	}
	if record != nil {
		l.revision = record.Revision
		l.loaded = true
	}
	return record, nil
}

func (l *Ledger) readLocked() (*engine.Ledger, error) {
	info, err := os.Stat(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat ledger: %w", err)
	}
	if info.Size() > MaxLedgerBytes {
		return nil, errors.New("ledger exceeds the size budget")
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	var record engine.Ledger
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, errors.New("ledger is corrupt")
	}
	if record.Format != "mobile-use-vnext-ledger" || record.Version != 1 {
		return nil, errors.New("ledger format is unsupported")
	}
	if record.Revision < 0 {
		return nil, errors.New("ledger revision is invalid")
	}
	return &record, nil
}

// Save durably writes the immutable snapshot and returns the assigned
// revision. The caller must not mutate record after the call.
func (l *Ledger) Save(ctx context.Context, record *engine.Ledger) (int64, error) {
	if record == nil {
		return 0, errors.New("ledger record is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !l.loaded {
		// Re-read once so an external writer cannot be silently overwritten.
		if _, err := l.readLocked(); err != nil {
			return 0, err
		}
		l.loaded = true
	}
	next := l.revision + 1
	record.Format = "mobile-use-vnext-ledger"
	record.Version = 1
	record.Revision = next
	data, err := json.Marshal(record)
	if err != nil {
		return 0, fmt.Errorf("encode ledger: %w", err)
	}
	if len(data) > MaxLedgerBytes {
		return 0, errors.New("ledger exceeds the size budget")
	}
	if err := atomicReplace(l.path, data); err != nil {
		return 0, err
	}
	l.revision = next
	return next, nil
}

// Revision returns the last durably committed revision.
func (l *Ledger) Revision() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.revision
}

func atomicReplace(path string, data []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".ledger-*.tmp")
	if err != nil {
		return fmt.Errorf("create ledger temp: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod ledger temp: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write ledger temp: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("fsync ledger temp: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close ledger temp: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace ledger: %w", err)
	}
	if runtime.GOOS != "windows" {
		parent, err := os.Open(dir)
		if err != nil {
			return fmt.Errorf("open ledger directory: %w", err)
		}
		defer parent.Close()
		if err := parent.Sync(); err != nil {
			return fmt.Errorf("fsync ledger directory: %w", err)
		}
	}
	return nil
}
