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
	// ErrTextResultInvalid means a patched text RPC returned a value whose
	// members or semantic combination cannot be a complete result of the fixed
	// helper. The engine classifies it as an unknown receipt, never as
	// not-dispatched or as a successful readback.
	ErrTextResultInvalid = errors.New("text rpc result is invalid")
)
