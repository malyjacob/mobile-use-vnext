// Package store implements the private recovery ledger as a single-writer,
// atomically replaced file. Durability follows the phone Debian chroot target:
// temp file -> fsync -> rename over the target -> fsync parent directory.
package store

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/malyjacob/mobile-use-vnext/internal/engine"
)

const (
	// MaxLedgerBytes bounds the single recovery object.
	MaxLedgerBytes = 64 * 1024

	ledgerFormat     = "mobile-use-vnext-ledger"
	ledgerVersionV1  = 1
	ledgerVersionV2  = 2
	maxDeviceIDLen   = 128
	maxCheckedTaskID = 256
)

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
	if err := json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
		return nil, errors.New("ledger is corrupt")
	}
	if record.Format != ledgerFormat ||
		(record.Version != ledgerVersionV1 && record.Version != ledgerVersionV2) {
		return nil, errors.New("ledger format is unsupported")
	}
	if record.Revision < 0 {
		return nil, errors.New("ledger revision is invalid")
	}
	if record.Version == ledgerVersionV1 {
		if hasVersionOneEvidence(data) {
			return nil, errors.New("version 1 ledger contains version 2 evidence")
		}
	} else {
		if hasExplicitLegacyUIPendingNull(data) {
			return nil, errors.New("ledger legacy ui pending cannot be null")
		}
		if err := validateEvidence(&record); err != nil {
			return nil, err
		}
	}
	return &record, nil
}

func hasVersionOneEvidence(data []byte) bool {
	var presence struct {
		UI              jsontext.Value `json:"ui"`
		LegacyUI        jsontext.Value `json:"legacy_ui_checkpoint"`
		LegacyUIPending jsontext.Value `json:"legacy_ui_pending"`
		Cleanup         *struct {
			CheckedTaskIDs jsontext.Value `json:"checked_task_ids"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return true
	}
	return len(presence.UI) > 0 || len(presence.LegacyUI) > 0 ||
		len(presence.LegacyUIPending) > 0 ||
		(presence.Cleanup != nil && len(presence.Cleanup.CheckedTaskIDs) > 0)
}

func hasExplicitLegacyUIPendingNull(data []byte) bool {
	var presence struct {
		LegacyUIPending jsontext.Value `json:"legacy_ui_pending"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return true
	}
	return bytes.Equal(bytes.TrimSpace(presence.LegacyUIPending), []byte("null"))
}

func validateEvidence(record *engine.Ledger) error {
	if ui := record.UI; ui != nil {
		if !validOperationID(ui.OperationID) || !validOperationID(ui.Origin) {
			return errors.New("ledger ui identity is invalid")
		}
		if ui.Epoch <= 0 || ui.Step == 0 {
			return errors.New("ledger ui generation is invalid")
		}
		switch ui.Kind {
		case "tap", "long_press", "swipe", "key", "launch", "focus", "text":
		default:
			return errors.New("ledger ui kind is invalid")
		}
		if ui.DisplayID < 0 || ui.DisplayGeneration == "" {
			return errors.New("ledger ui display is invalid")
		}
		if !validDeviceID(ui.DeviceID) || !engine.ValidateUUID(ui.BootID) {
			return errors.New("ledger ui device identity is invalid")
		}
		switch ui.State {
		case "may_dispatch", "terminated", "not_dispatched":
		default:
			return errors.New("ledger ui state is invalid")
		}
	}
	if checkpoint := record.LegacyUI; checkpoint != nil {
		if !validOperationID(checkpoint.Origin) ||
			!validDeviceID(checkpoint.DeviceID) ||
			!engine.ValidateUUID(checkpoint.BootID) {
			return errors.New("ledger ui checkpoint is invalid")
		}
	}
	if record.Cleanup != nil {
		if err := validateCheckedTaskIDs(record.Cleanup.CheckedTaskIDs); err != nil {
			return err
		}
	}
	return nil
}

func validOperationID(id string) bool {
	if len(id) < 2 || len(id) > 64 || id[0] != 'P' {
		return false
	}
	for i := 1; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func validDeviceID(id string) bool {
	return id != "" && len(id) <= maxDeviceIDLen
}

func validateCheckedTaskIDs(ids []int) error {
	if len(ids) > maxCheckedTaskID {
		return errors.New("ledger checked task ids exceed the limit")
	}
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return errors.New("ledger checked task id is invalid")
		}
		if _, ok := seen[id]; ok {
			return errors.New("ledger checked task id is duplicated")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func copyLedgerRecord(record *engine.Ledger) *engine.Ledger {
	snapshot := *record
	return &snapshot
}

// Save durably writes an immutable snapshot and returns the assigned revision.
// It does not modify the caller's record or nested evidence.
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
		existing, err := l.readLocked()
		if err != nil {
			return 0, err
		}
		if existing != nil {
			l.revision = existing.Revision
		}
		l.loaded = true
	}
	next := l.revision + 1
	snapshot := copyLedgerRecord(record)
	snapshot.Format = ledgerFormat
	snapshot.Version = ledgerVersionV2
	snapshot.Revision = next
	if err := validateEvidence(snapshot); err != nil {
		return 0, err
	}
	data, err := json.Marshal(snapshot)
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
