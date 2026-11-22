package engine

// activeBaselineForTest returns the confirmed baseline the projector's current
// continuity state is aligned with, or nil. It is the test-only view of the
// bounded baseline pool.
func activeBaselineForTest(e *Engine) *baselineState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return baselineWithID(e.baselines, e.activeBaselineID)
}

// baselinePoolForTest copies the confirmed baseline pool for assertions that
// must not observe a later mutation.
func baselinePoolForTest(e *Engine) []*baselineState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*baselineState(nil), e.baselines...)
}
