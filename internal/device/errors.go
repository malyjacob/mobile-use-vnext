package device

import "errors"

// Managed shell start outcomes. They are device-layer facts, not transport
// errors: the runner metadata proves whether the user command was never
// started, or whether no start/finish evidence was readable in the bounded
// window. The android adapter returns exactly these values so the engine can
// classify the outcome without importing the adapter package.
var (
	// ErrShellNotStarted means the runner directory exists but the command was
	// provably never started.
	ErrShellNotStarted = errors.New("managed shell never started")
	// ErrShellStartUnconfirmed means the dispatch was issued but no start or
	// finish evidence was readable within the bounded window.
	ErrShellStartUnconfirmed = errors.New("managed shell start is unconfirmed")
)
