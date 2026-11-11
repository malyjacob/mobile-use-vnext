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
	// MaxRetiredBytes bounds the serialized retired slice, excluding the rest
	// of the current ownership record.
	MaxRetiredBytes = 24 * 1024
	// MaxBaseBytes bounds the serialized ledger excluding the retired and
	// task_transfers arrays.
	MaxBaseBytes = 20 * 1024
	// MaxTaskTransferBytes bounds the serialized task_transfers array.
	MaxTaskTransferBytes = 16 * 1024
	// MaxTaskTransferEntryBytes bounds one serialized task transfer.
	MaxTaskTransferEntryBytes = 1000
	// MaxRootWrapperBytes bounds the root object members that join the base to
	// the two arrays: property names, separators and braces.
	MaxRootWrapperBytes = 512
	// MaxTaskTransfers bounds the number of task transfer records.
	MaxTaskTransfers = 16

	ledgerFormat      = "mobile-use-vnext-ledger"
	ledgerVersionV1   = 1
	ledgerVersionV2   = 2
	ledgerVersionV3   = 3
	ledgerVersionV4   = 4
	ledgerVersionV5   = 5
	maxDeviceIDLen    = 128
	maxCheckedTaskID  = 256
	maxRetiredEntries = 8
	maxTaskTransfers  = MaxTaskTransfers
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
	if len(data) > MaxLedgerBytes {
		return nil, errors.New("ledger exceeds the size budget")
	}
	return decodeLedger(data, ledgerVersionV5)
}

// decodeLedger parses and validates one encoded record while accepting at most
// maxVersion. Load uses the current maximum; tests use it to exercise the
// controlled rejection of an older reader without a second implementation.
func decodeLedger(data []byte, maxVersion int) (*engine.Ledger, error) {
	var record engine.Ledger
	if err := json.Unmarshal(data, &record, json.RejectUnknownMembers(true)); err != nil {
		return nil, errors.New("ledger is corrupt")
	}
	if record.Format != ledgerFormat ||
		record.Version < ledgerVersionV1 || record.Version > maxVersion {
		return nil, errors.New("ledger format is unsupported")
	}
	if record.Revision < 0 {
		return nil, errors.New("ledger revision is invalid")
	}
	if record.Version < ledgerVersionV4 && hasVersionFourEvidence(data) {
		return nil, fmt.Errorf("version %d ledger contains version 4 evidence", record.Version)
	}
	if record.Version < ledgerVersionV5 && hasVersionFiveEvidence(data) {
		return nil, fmt.Errorf("version %d ledger contains version 5 evidence", record.Version)
	}
	switch record.Version {
	case ledgerVersionV1:
		if hasVersionOneEvidence(data) || hasVersionThreeEvidence(data) {
			return nil, errors.New("version 1 ledger contains later-version evidence")
		}
	case ledgerVersionV2:
		if hasVersionThreeEvidence(data) {
			return nil, errors.New("version 2 ledger contains version 3 evidence")
		}
		if hasExplicitEvidenceBooleanNull(data) {
			return nil, errors.New("ledger evidence boolean cannot be null")
		}
		if err := validateEvidence(&record); err != nil {
			return nil, err
		}
	case ledgerVersionV3, ledgerVersionV4:
		if hasExplicitEvidenceBooleanNull(data) {
			return nil, errors.New("ledger evidence boolean cannot be null")
		}
		if err := validateEvidence(&record); err != nil {
			return nil, err
		}
		if err := validateRetiredRecord(&record); err != nil {
			return nil, err
		}
		if err := validateRetiredJSON(data); err != nil {
			return nil, err
		}
	case ledgerVersionV5:
		if hasExplicitEvidenceBooleanNull(data) {
			return nil, errors.New("ledger evidence boolean cannot be null")
		}
		if err := validateEvidence(&record); err != nil {
			return nil, err
		}
		if err := validateRetiredRecord(&record); err != nil {
			return nil, err
		}
		if err := validateRetiredJSON(data); err != nil {
			return nil, err
		}
		if err := validateTaskTransferJSON(data); err != nil {
			return nil, err
		}
		if err := validateTaskTransfers(&record); err != nil {
			return nil, err
		}
		if err := validateLedgerCapacity(&record); err != nil {
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
			CheckedTaskIDs           jsontext.Value `json:"checked_task_ids"`
			ExecutionRecheckRequired jsontext.Value `json:"execution_recheck_required"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return true
	}
	return len(presence.UI) > 0 || len(presence.LegacyUI) > 0 ||
		len(presence.LegacyUIPending) > 0 ||
		(presence.Cleanup != nil && (len(presence.Cleanup.CheckedTaskIDs) > 0 || len(presence.Cleanup.ExecutionRecheckRequired) > 0))
}

func hasExplicitEvidenceBooleanNull(data []byte) bool {
	var presence struct {
		DroppedUnconfirmed jsontext.Value `json:"dropped_unconfirmed"`
		LegacyUIPending    jsontext.Value `json:"legacy_ui_pending"`
		Cleanup            *struct {
			ExecutionRecheckRequired jsontext.Value `json:"execution_recheck_required"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return true
	}
	return bytes.Equal(bytes.TrimSpace(presence.DroppedUnconfirmed), []byte("null")) || bytes.Equal(bytes.TrimSpace(presence.LegacyUIPending), []byte("null")) ||
		presence.Cleanup != nil && bytes.Equal(bytes.TrimSpace(presence.Cleanup.ExecutionRecheckRequired), []byte("null"))
}

func hasVersionThreeEvidence(data []byte) bool {
	var presence struct {
		DroppedUnconfirmed jsontext.Value `json:"dropped_unconfirmed"`
		Retired            jsontext.Value `json:"retired"`
		Session            *struct {
			CreateCommand jsontext.Value `json:"create_command"`
			DeviceID      jsontext.Value `json:"device_id"`
			BootID        jsontext.Value `json:"boot_id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return true
	}
	return len(presence.DroppedUnconfirmed) > 0 || len(presence.Retired) > 0 ||
		presence.Session != nil &&
			(len(presence.Session.CreateCommand) > 0 ||
				len(presence.Session.DeviceID) > 0 ||
				len(presence.Session.BootID) > 0)
}

func hasVersionFourEvidence(data []byte) bool {
	var presence struct {
		UI *struct {
			State jsontext.Value `json:"state"`
		} `json:"ui"`
		Retired []struct {
			Record jsontext.Value `json:"record"`
		} `json:"retired"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return true
	}
	if presence.UI != nil && bytes.Equal(bytes.TrimSpace(presence.UI.State), []byte(`"unknown"`)) {
		return true
	}
	for _, retired := range presence.Retired {
		if hasVersionFourEvidence(retired.Record) {
			return true
		}
	}
	return false
}

func hasVersionFiveEvidence(data []byte) bool {
	var presence struct {
		TaskTransfers jsontext.Value `json:"task_transfers"`
		Tasks         []struct {
			RootTaskID    jsontext.Value `json:"root_task_id"`
			Ownership     jsontext.Value `json:"ownership"`
			OriginDisplay jsontext.Value `json:"origin_display"`
			Generation    jsontext.Value `json:"generation"`
		} `json:"tasks"`
		Retired []struct {
			Record jsontext.Value `json:"record"`
		} `json:"retired"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return true
	}
	if len(presence.TaskTransfers) > 0 {
		return true
	}
	for _, task := range presence.Tasks {
		if len(task.RootTaskID) > 0 || len(task.Ownership) > 0 ||
			len(task.OriginDisplay) > 0 || len(task.Generation) > 0 {
			return true
		}
	}
	for _, retired := range presence.Retired {
		if hasVersionFiveEvidence(retired.Record) {
			return true
		}
	}
	return false
}

func validateEvidence(record *engine.Ledger) error {
	if session := record.Session; session != nil {
		if session.DeviceID != "" && !validDeviceID(session.DeviceID) {
			return errors.New("ledger session device identity is invalid")
		}
		if session.BootID != "" && !engine.ValidateUUID(session.BootID) {
			return errors.New("ledger session boot identity is invalid")
		}
	}
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
		case "may_dispatch", "terminated", "not_dispatched", "unknown":
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

func validateRetiredRecord(record *engine.Ledger) error {
	if len(record.Retired) > maxRetiredEntries {
		return errors.New("ledger retired entries exceed the limit")
	}
	if record.Retired != nil {
		data, err := json.Marshal(record.Retired)
		if err != nil {
			return errors.New("ledger retired entries are invalid")
		}
		if len(data) > MaxRetiredBytes {
			return errors.New("ledger retired entries exceed the size budget")
		}
	}
	for _, retired := range record.Retired {
		if retired.Record == nil {
			return errors.New("ledger retired record is required")
		}
		if retired.Record.Retired != nil {
			return errors.New("ledger retired record is recursive")
		}
		if retired.Record.TaskTransfers != nil {
			return errors.New("ledger retired record contains task transfers")
		}
		if retired.Attempts < 0 || retired.Attempts > 3 {
			return errors.New("ledger retired attempts are invalid")
		}
		if err := validateEvidence(retired.Record); err != nil {
			return err
		}
	}
	return nil
}

// validateTaskTransferJSON rejects explicit null where the record shape would
// otherwise silently collapse to an absent array.
func validateTaskTransferJSON(data []byte) error {
	var presence struct {
		TaskTransfers jsontext.Value `json:"task_transfers"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return errors.New("ledger task transfers are invalid")
	}
	value := bytes.TrimSpace(presence.TaskTransfers)
	if len(value) == 0 {
		return nil
	}
	if bytes.Equal(value, []byte("null")) {
		return errors.New("ledger task transfers cannot be null")
	}
	return nil
}

func validateTaskTransfers(record *engine.Ledger) error {
	if len(record.TaskTransfers) > maxTaskTransfers {
		return errors.New("ledger task transfers exceed the limit")
	}
	if len(record.TaskTransfers) > 0 {
		data, err := json.Marshal(record.TaskTransfers)
		if err != nil {
			return errors.New("ledger task transfers are invalid")
		}
		if len(data) > MaxTaskTransferBytes {
			return errors.New("ledger task transfers exceed the size budget")
		}
	}
	for _, transfer := range record.TaskTransfers {
		data, err := json.Marshal(transfer)
		if err != nil {
			return errors.New("ledger task transfer is invalid")
		}
		if len(data) > MaxTaskTransferEntryBytes {
			return errors.New("ledger task transfer exceeds the size budget")
		}
	}
	return nil
}

// validateLedgerCapacity applies the independent v5 budgets in addition to the
// complete encoding gate. The base copy clears both bounded arrays; their exact
// encoded lengths and the remaining root wrapper are measured separately.
func validateLedgerCapacity(record *engine.Ledger) error {
	base := *record
	base.Retired = nil
	base.TaskTransfers = nil
	baseData, err := json.Marshal(&base)
	if err != nil {
		return errors.New("ledger base is invalid")
	}
	if len(baseData) > MaxBaseBytes {
		return errors.New("ledger base exceeds the size budget")
	}
	retiredBytes := 0
	if len(record.Retired) > 0 {
		data, err := json.Marshal(record.Retired)
		if err != nil {
			return errors.New("ledger retired entries are invalid")
		}
		retiredBytes = len(data)
	}
	transferBytes := 0
	if len(record.TaskTransfers) > 0 {
		data, err := json.Marshal(record.TaskTransfers)
		if err != nil {
			return errors.New("ledger task transfers are invalid")
		}
		transferBytes = len(data)
	}
	fullData, err := json.Marshal(record)
	if err != nil {
		return errors.New("ledger encoding is invalid")
	}
	if len(fullData) > MaxLedgerBytes {
		return errors.New("ledger exceeds the size budget")
	}
	if wrapper := len(fullData) - len(baseData) - retiredBytes - transferBytes; wrapper > MaxRootWrapperBytes {
		return errors.New("ledger root wrapper exceeds the size budget")
	}
	return nil
}

func validateRetiredJSON(data []byte) error {
	if err := validateVersionThreeSessionJSON(data); err != nil {
		return err
	}
	var presence struct {
		Retired jsontext.Value `json:"retired"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return errors.New("ledger retired entries are invalid")
	}
	retiredJSON := bytes.TrimSpace(presence.Retired)
	if len(retiredJSON) == 0 {
		return nil
	}
	if bytes.Equal(retiredJSON, []byte("null")) {
		return errors.New("ledger retired entries cannot be null")
	}
	var entries []struct {
		Record        jsontext.Value `json:"record"`
		BudgetIssued  jsontext.Value `json:"budget_issued"`
		StoppedReason jsontext.Value `json:"stopped_reason"`
		Attempts      jsontext.Value `json:"attempts"`
	}
	if err := json.Unmarshal(retiredJSON, &entries); err != nil {
		return errors.New("ledger retired entries are invalid")
	}
	for _, entry := range entries {
		recordJSON := bytes.TrimSpace(entry.Record)
		if len(recordJSON) == 0 || bytes.Equal(recordJSON, []byte("null")) {
			return errors.New("ledger retired record is required")
		}
		budgetIssued := bytes.TrimSpace(entry.BudgetIssued)
		if !bytes.Equal(budgetIssued, []byte("true")) &&
			!bytes.Equal(budgetIssued, []byte("false")) {
			return errors.New("ledger retired budget marker is required")
		}
		attempts := bytes.TrimSpace(entry.Attempts)
		if len(attempts) == 0 || bytes.Equal(attempts, []byte("null")) {
			return errors.New("ledger retired attempts are required")
		}
		stoppedReason := bytes.TrimSpace(entry.StoppedReason)
		if bytes.Equal(stoppedReason, []byte("null")) {
			return errors.New("ledger retired stop reason cannot be null")
		}
		var nested struct {
			Retired       jsontext.Value `json:"retired"`
			TaskTransfers jsontext.Value `json:"task_transfers"`
		}
		if err := json.Unmarshal(recordJSON, &nested); err != nil {
			return errors.New("ledger retired record is invalid")
		}
		if len(nested.Retired) > 0 {
			return errors.New("ledger retired record is recursive")
		}
		if len(nested.TaskTransfers) > 0 {
			return errors.New("ledger retired record contains task transfers")
		}
		if hasExplicitEvidenceBooleanNull(recordJSON) {
			return errors.New("ledger evidence boolean cannot be null")
		}
		if err := validateVersionThreeSessionJSON(recordJSON); err != nil {
			return err
		}
	}
	return nil
}

func validateVersionThreeSessionJSON(data []byte) error {
	var presence struct {
		Session *struct {
			CreateCommand jsontext.Value `json:"create_command"`
			DeviceID      jsontext.Value `json:"device_id"`
			BootID        jsontext.Value `json:"boot_id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return errors.New("ledger session is invalid")
	}
	if presence.Session == nil {
		return nil
	}
	for _, value := range []jsontext.Value{
		presence.Session.CreateCommand,
		presence.Session.DeviceID,
		presence.Session.BootID,
	} {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("ledger session identity cannot be null")
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
	if record.Version > ledgerVersionV5 {
		return 0, errors.New("ledger version is unsupported")
	}
	if err := validateEvidence(record); err != nil {
		return 0, err
	}
	if err := validateRetiredRecord(record); err != nil {
		return 0, err
	}
	if err := validateTaskTransfers(record); err != nil {
		return 0, err
	}
	next := l.revision + 1
	snapshot := engine.CloneLedger(record)
	snapshot.Format = ledgerFormat
	snapshot.Version = ledgerVersionV5
	snapshot.Revision = next
	if err := validateLedgerCapacity(snapshot); err != nil {
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
