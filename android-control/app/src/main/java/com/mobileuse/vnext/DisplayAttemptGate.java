package com.mobileuse.vnext;

/** Atomic cancellation/dispatch boundary for one allocation. Cancellation before dispatch
 * proves no CREATE was sent; after dispatch, cleanup must wait for the helper death barrier.
 * No locks are held during Binder or device calls, so STOP never waits for those calls. */
final class DisplayAttemptGate {
    private boolean cancelled;
    private boolean dispatched;
    synchronized boolean tryDispatch() {
        if (cancelled || dispatched) return false;
        dispatched = true;
        return true;
    }
    synchronized void cancel() { cancelled = true; }
    synchronized boolean cancelled() { return cancelled; }
    synchronized boolean dispatched() { return dispatched; }
    synchronized boolean cancelledBeforeDispatch() { return cancelled && !dispatched; }
}
