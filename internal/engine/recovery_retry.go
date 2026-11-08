package engine

import "context"

// RetryRecovery re-evaluates durable old ownership after the device becomes
// available. It is safe to call repeatedly; a resolved record stays cleared.
func (e *Engine) RetryRecovery(ctx context.Context) {
	e.driveRecovery(ctx)
}
