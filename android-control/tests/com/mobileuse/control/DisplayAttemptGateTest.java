package com.mobileuse.vnext;

/** Pure JVM checks for the display dispatch gate used by ControlService. */
public final class DisplayAttemptGateTest {
    public static void main(String[] args) {
        cancelBeforeDispatchBlocksCreate();
        dispatchIsSingleShot();
        cancelAfterDispatchWaitsForTerminalEvidence();
        deathSettlesDispatchedAttempt();
        deathAfterCancelSettlesCreateWindow();
        System.out.println("DisplayAttemptGateTest passed");
    }

    private static void cancelBeforeDispatchBlocksCreate() {
        DisplayAttemptGate gate = new DisplayAttemptGate();
        gate.cancel();
        check(!gate.tryDispatch(), "cancelled attempt must not dispatch");
        check(gate.cancelledBeforeDispatch(), "cancel before dispatch is proveable");
        check(!gate.dispatched(), "no dispatch recorded");
    }

    private static void dispatchIsSingleShot() {
        DisplayAttemptGate gate = new DisplayAttemptGate();
        check(gate.tryDispatch(), "first dispatch is accepted");
        check(!gate.tryDispatch(), "second dispatch is refused");
        check(!gate.cancelledBeforeDispatch(), "dispatched attempt cannot claim cancellation");
    }

    private static void cancelAfterDispatchWaitsForTerminalEvidence() {
        DisplayAttemptGate gate = new DisplayAttemptGate();
        check(gate.tryDispatch(), "dispatch is accepted");
        gate.cancel();
        check(!gate.cancelledBeforeDispatch(), "local cancellation is not a remote termination proof");
        check(!gate.settled(), "dispatched cancellation keeps the resource observation window open");
        check(gate.settle(), "first terminal callback settles the dispatch");
        check(gate.settled(), "callback terminal evidence is recorded");
        check(!gate.settle(), "terminal callback is single-shot");
    }

    private static void deathSettlesDispatchedAttempt() {
        DisplayAttemptGate gate = new DisplayAttemptGate();
        check(gate.tryDispatch(), "dispatch is accepted");
        gate.settleByDeath();
        check(gate.settled(), "helper death terminates an in-flight dispatch");
    }

    private static void deathAfterCancelSettlesCreateWindow() {
        DisplayAttemptGate gate = new DisplayAttemptGate();
        check(gate.tryDispatch(), "dispatch is accepted");
        gate.cancel();
        check(!gate.settled(), "local cancel cannot settle an in-flight CREATE");
        gate.settleByDeath();
        check(gate.settled(), "helper death releases the App reader barrier");
    }

    private static void check(boolean condition, String message) {
        if (!condition) {
            throw new AssertionError(message);
        }
    }
}
