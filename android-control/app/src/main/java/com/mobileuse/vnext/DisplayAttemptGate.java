package com.mobileuse.vnext;

/** Atomic cancellation/dispatch boundary for one allocation. Cancellation before dispatch
 * proves no CREATE was sent; after dispatch, cleanup must wait for the helper death barrier.
 * No locks are held during Binder or device calls, so STOP never waits for those calls. */
final class DisplayAttemptGate {
    private boolean cancelled;
    private boolean dispatched;
    private boolean settled;

    synchronized boolean tryDispatch() {
        if (cancelled || dispatched || settled) return false;
        dispatched = true;
        return true;
    }

    synchronized void cancel() { cancelled = true; }

    /** A result callback closes this attempt's CREATE observation window. */
    synchronized boolean settle() {
        if (!dispatched || settled) return false;
        settled = true;
        return true;
    }

    /** Helper death terminates a dispatched CREATE even when no result callback arrives. */
    synchronized void settleByDeath() { settled = true; }

    synchronized boolean cancelled() { return cancelled; }
    synchronized boolean dispatched() { return dispatched; }
    synchronized boolean cancelledBeforeDispatch() { return cancelled && !dispatched; }
    synchronized boolean settled() { return settled; }
}
