package engine

import "time"

// Ledger is the minimal private recovery record. It holds only trusted
// ownership, command and resource evidence; it never stores request bodies,
// trees, images, input text, commands or shell output. The single serialized
// object is bounded to 64 KiB by the store.
//
// Version 4 adds the closed input window state "unknown": a dispatch call
// returned without a usable end receipt, so the window is over while the end
// fact stays unproven.
type Ledger struct {
	DroppedUnconfirmed uint64         `json:"dropped_unconfirmed,omitzero"`
	Format             string         `json:"format"`
	Version            int            `json:"version"`
	Revision           int64          `json:"revision"`
	ServiceInstanceID  string         `json:"service_instance_id"`
	UpdatedAt          time.Time      `json:"updated_at"`
	AppInstanceID      string         `json:"app_instance_id,omitempty"`
	Session            *LedgerSession `json:"session,omitempty"`
	PendingCreate      *LedgerCommand `json:"pending_create,omitempty"`
	PendingClose       *LedgerCommand `json:"pending_close,omitempty"`
	Cleanup            *LedgerCleanup `json:"cleanup,omitempty"`
	// Tasks are exact owned window identities for virtual sessions. The list
	// is bounded and contains no window content.
	Tasks []LedgerTask `json:"tasks,omitempty"`
	// Stop carries the bound stop boundary so a restart keeps the admission
	// barrier even when the original App instance is gone.
	Stop *LedgerStop `json:"stop,omitempty"`
	// Shell is managed runner ownership retained for exact bounded cleanup and
	// late termination evidence. It carries identity, phase and bounded
	// counters only: never the command, its output, hashes or error strings.
	Shell *LedgerShell `json:"shell,omitempty"`
	// UI is the durable identity and minimum end receipt of one UI input
	// dispatch. It carries ownership and evidence state only, never input text
	// or command content.
	UI *LedgerUI `json:"ui,omitempty"`
	// LegacyUI records the original device and boot identity used to resolve a
	// version 1 UI execution responsibility.
	LegacyUI *LedgerUICheckpoint `json:"legacy_ui_checkpoint,omitempty"`
	// LegacyUIPending preserves the need to establish a legacy UI checkpoint
	// before any later save can erase the original version-one evidence.
	LegacyUIPending bool `json:"legacy_ui_pending,omitempty"`
	// Retired holds the bounded ownership records that no longer have ordinary
	// dispatch authority but may still need exact cleanup.
	Retired []LedgerRetired `json:"retired,omitempty"`
	// TaskTransfers is the root-level protection pool of prepared and confirmed
	// task transfers. Every delete path consults it; retired records never
	// embed it.
	TaskTransfers []LedgerTransfer `json:"task_transfers,omitempty"`
}

// Task transfer directions and states.
const (
	TransferIn  = "in"
	TransferOut = "out"

	TransferPrepared      = "prepared"
	TransferMayDispatch   = "may_dispatch"
	TransferTerminated    = "terminated"
	TransferNotDispatched = "not_dispatched"
	TransferUnknown       = "unknown"
	TransferReleased      = "released"
)

// LedgerTransfer is one durable task transfer responsibility. It records only
// identity and state, never the request, a tree or free text.
type LedgerTransfer struct {
	OperationID      string    `json:"operation_id"`
	Direction        string    `json:"direction"`
	State            string    `json:"state"`
	Epoch            int64     `json:"epoch,omitempty"`
	BootID           string    `json:"boot_id,omitempty"`
	AppInstanceID    string    `json:"app_instance_id,omitempty"`
	Package          string    `json:"package"`
	LeafTaskID       int       `json:"leaf_task_id"`
	RootTaskID       int       `json:"root_task_id,omitempty"`
	SourceDisplayID  int       `json:"source_display_id"`
	SourceGeneration string    `json:"source_generation,omitempty"`
	TargetDisplayID  int       `json:"target_display_id"`
	CreatedAt        time.Time `json:"created_at"`
}

// LedgerRetired wraps one frozen ownership record with the bounded cleanup
// budget facts. The nested record must not contain another Retired slice.
type LedgerRetired struct {
	Record        *Ledger `json:"record"`
	BudgetIssued  bool    `json:"budget_issued"`
	StoppedReason string  `json:"stopped_reason,omitempty"`
	Attempts      int     `json:"attempts"`
}

// LedgerShell is the durable identity and minimum end receipt of one managed
// shell run. CommandRunes is a length, not the command.
type LedgerShell struct {
	RunnerID          string    `json:"runner_id"`
	Dir               string    `json:"dir,omitempty"`
	OperationID       string    `json:"operation_id"`
	Agent             string    `json:"agent,omitempty"`
	Origin            string    `json:"origin,omitempty"`
	Epoch             int64     `json:"epoch,omitempty"`
	AppInstanceID     string    `json:"app_instance_id,omitempty"`
	ModeGeneration    string    `json:"mode_generation,omitempty"`
	DisplayID         int       `json:"display_id,omitempty"`
	DisplayGeneration string    `json:"display_generation,omitempty"`
	Root              bool      `json:"root"`
	State             string    `json:"state"`
	Phase             string    `json:"phase,omitempty"`
	CommandRunes      int       `json:"command_runes"`
	StartedAt         time.Time `json:"started_at"`
	DeadlineAt        time.Time `json:"deadline_at"`
	Termination       string    `json:"termination,omitempty"`
	ExitCode          *int      `json:"exit_code,omitempty"`
	OutputBytes       int       `json:"output_bytes"`
	Truncated         *bool     `json:"truncated,omitempty"`
	ConfirmedAt       time.Time `json:"confirmed_at,omitempty"`
	// Start identity of the managed process group, recorded after the runner
	// reported it. Recovery uses it to prove that an alive PID is not a reused
	// one; it is not user content.
	StartPID   int    `json:"start_pid,omitempty"`
	StartPGID  int    `json:"start_pgid,omitempty"`
	StartTicks uint64 `json:"start_ticks,omitempty"`
	BootID     string `json:"boot_id,omitempty"`
}

// LedgerSession is the durable ownership record of one session attempt.
type LedgerSession struct {
	Origin            string    `json:"origin"`
	Agent             string    `json:"agent"`
	Mode              string    `json:"mode"`
	State             string    `json:"state"`
	AppInstanceID     string    `json:"app_instance_id"`
	ModeGeneration    string    `json:"mode_generation"`
	CreateCommand     string    `json:"create_command,omitempty"`
	DeviceID          string    `json:"device_id,omitempty"`
	BootID            string    `json:"boot_id,omitempty"`
	DisplayID         int       `json:"display_id"`
	SFID              uint64    `json:"sf_id"`
	DisplayGeneration string    `json:"display_generation"`
	Width             int       `json:"width"`
	Height            int       `json:"height"`
	Density           int       `json:"density"`
	Rotation          int       `json:"rotation"`
	StartedAt         time.Time `json:"started_at"`
}

// LedgerTask is one exact window identity with its ownership evidence. It
// carries no window content.
type LedgerTask struct {
	TaskID    int    `json:"task_id"`
	Package   string `json:"package"`
	DisplayID int    `json:"display_id"`
	// RootTaskID is the containing root task id; zero means the source never
	// reported one and the task is not movable.
	RootTaskID int `json:"root_task_id,omitempty"`
	// Ownership is owned, borrowed or shared. A version 1-4 task upgrades to
	// owned; no package-name inference is ever applied.
	Ownership string `json:"ownership,omitempty"`
	// OriginDisplay is the original-ownership display of a borrowed task.
	OriginDisplay *int   `json:"origin_display,omitempty"`
	Generation    string `json:"generation,omitempty"`
	Proven        bool   `json:"proven"`
}

// LedgerUI is one durable UI input responsibility. State records the dispatch
// window only (may_dispatch while the call is in flight, then terminated,
// not_dispatched or unknown); it never represents the tool's primary outcome.
type LedgerUI struct {
	OperationID       string `json:"operation_id"`
	Origin            string `json:"origin"`
	Epoch             int64  `json:"epoch"`
	Step              uint64 `json:"step"`
	Kind              string `json:"kind"`
	DisplayID         int    `json:"display_id"`
	DisplayGeneration string `json:"display_generation"`
	DeviceID          string `json:"device_id"`
	BootID            string `json:"boot_id"`
	State             string `json:"state"`
}

// LedgerUICheckpoint is the immutable startup boundary for resolving a
// version 1 UI execution responsibility that lacks dispatch-end evidence.
type LedgerUICheckpoint struct {
	Origin   string `json:"origin"`
	DeviceID string `json:"device_id"`
	BootID   string `json:"boot_id"`
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
	State     string `json:"state"`
	Execution string `json:"execution"`
	Tasks     string `json:"tasks"`
	Recents   string `json:"recents"`
	Display   string `json:"display"`
	Reason    string `json:"reason,omitempty"`
	// ExecReason records the source of an execution uncertainty
	// (ui_input / shell_chain) so a restart can still tell whether exact-chain
	// release evidence may promote it.
	ExecReason string    `json:"exec_reason,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	// CheckedTaskIDs is bounded cleanup evidence discovered on the original
	// display. It is not authority or a live task registry.
	CheckedTaskIDs []int `json:"checked_task_ids,omitempty"`
	// ExecutionRecheckRequired invalidates physical evidence collected before
	// the last execution boundary. Only a later physical recheck clears it.
	ExecutionRecheckRequired bool `json:"execution_recheck_required,omitzero"`
}

// LedgerStop is the bound minimal-STOP boundary.
type LedgerStop struct {
	AppInstanceID string    `json:"app_instance_id,omitempty"`
	Seq           int64     `json:"seq"`
	AcceptedAt    time.Time `json:"accepted_at"`
}

// CloneLedger returns a deep copy of the ownership record. Retired entries
// copy one nested record level only because recursive retirement is invalid.
func CloneLedger(record *Ledger) *Ledger {
	return cloneLedger(record, true)
}

func cloneLedger(record *Ledger, includeRetired bool) *Ledger {
	if record == nil {
		return nil
	}
	clone := *record
	if record.Session != nil {
		value := *record.Session
		clone.Session = &value
	}
	if record.PendingCreate != nil {
		value := *record.PendingCreate
		clone.PendingCreate = &value
	}
	if record.PendingClose != nil {
		value := *record.PendingClose
		clone.PendingClose = &value
	}
	if record.Cleanup != nil {
		value := *record.Cleanup
		if value.CheckedTaskIDs != nil {
			value.CheckedTaskIDs = append([]int{}, value.CheckedTaskIDs...)
		}
		clone.Cleanup = &value
	}
	if record.Tasks != nil {
		clone.Tasks = make([]LedgerTask, len(record.Tasks))
		copy(clone.Tasks, record.Tasks)
		for i := range record.Tasks {
			if record.Tasks[i].OriginDisplay != nil {
				origin := *record.Tasks[i].OriginDisplay
				clone.Tasks[i].OriginDisplay = &origin
			}
		}
	}
	if record.TaskTransfers != nil {
		clone.TaskTransfers = append([]LedgerTransfer{}, record.TaskTransfers...)
	}
	if record.Stop != nil {
		value := *record.Stop
		clone.Stop = &value
	}
	if record.Shell != nil {
		value := *record.Shell
		if value.ExitCode != nil {
			exitCode := *value.ExitCode
			value.ExitCode = &exitCode
		}
		if value.Truncated != nil {
			truncated := *value.Truncated
			value.Truncated = &truncated
		}
		clone.Shell = &value
	}
	if record.UI != nil {
		value := *record.UI
		clone.UI = &value
	}
	if record.LegacyUI != nil {
		value := *record.LegacyUI
		clone.LegacyUI = &value
	}
	if includeRetired && record.Retired != nil {
		clone.Retired = make([]LedgerRetired, len(record.Retired))
		for i, retired := range record.Retired {
			clone.Retired[i] = retired
			clone.Retired[i].Record = cloneLedger(retired.Record, false)
		}
	} else {
		clone.Retired = nil
	}
	return &clone
}

// Ledger format constants.
const (
	ledgerFormat  = "mobile-use-vnext-ledger"
	ledgerVersion = 5
)
