package engine

import "time"

// Ledger is the minimal private recovery record. It holds only trusted
// ownership, command and resource evidence; it never stores request bodies,
// trees, images, input text, commands or shell output. The single serialized
// object is bounded to 64 KiB by the store.
type Ledger struct {
	Format            string         `json:"format"`
	Version           int            `json:"version"`
	Revision          int64          `json:"revision"`
	ServiceInstanceID string         `json:"service_instance_id"`
	UpdatedAt         time.Time      `json:"updated_at"`
	AppInstanceID     string         `json:"app_instance_id,omitempty"`
	Session           *LedgerSession `json:"session,omitempty"`
	PendingCreate     *LedgerCommand `json:"pending_create,omitempty"`
	PendingClose      *LedgerCommand `json:"pending_close,omitempty"`
	Cleanup           *LedgerCleanup `json:"cleanup,omitempty"`
	// Stop carries the bound stop boundary so a restart keeps the admission
	// barrier even when the original App instance is gone.
	Stop *LedgerStop `json:"stop,omitempty"`
}

// LedgerSession is the durable ownership record of one session attempt.
type LedgerSession struct {
	Origin            string    `json:"origin"`
	Agent             string    `json:"agent"`
	Mode              string    `json:"mode"`
	State             string    `json:"state"`
	AppInstanceID     string    `json:"app_instance_id"`
	ModeGeneration    string    `json:"mode_generation"`
	DisplayID         int       `json:"display_id"`
	SFID              uint64    `json:"sf_id"`
	DisplayGeneration string    `json:"display_generation"`
	Width             int       `json:"width"`
	Height            int       `json:"height"`
	Density           int       `json:"density"`
	Rotation          int       `json:"rotation"`
	StartedAt         time.Time `json:"started_at"`
}

// LedgerCommand is one display create/close intent saved before it is sent.
type LedgerCommand struct {
	ID                string    `json:"id"`
	Kind              string    `json:"kind"`
	AppInstanceID     string    `json:"app_instance_id"`
	DisplayID         int       `json:"display_id"`
	DisplayGeneration string    `json:"display_generation"`
	RequestedAt       time.Time `json:"requested_at"`
}

// LedgerCleanup is the durable closure progress of the original resources.
type LedgerCleanup struct {
	State     string    `json:"state"`
	Execution string    `json:"execution"`
	Tasks     string    `json:"tasks"`
	Recents   string    `json:"recents"`
	Display   string    `json:"display"`
	Reason    string    `json:"reason,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// LedgerStop is the bound minimal-STOP boundary.
type LedgerStop struct {
	AppInstanceID string    `json:"app_instance_id,omitempty"`
	Seq           int64     `json:"seq"`
	AcceptedAt    time.Time `json:"accepted_at"`
}

// Ledger format constants.
const (
	ledgerFormat  = "mobile-use-vnext-ledger"
	ledgerVersion = 1
)
