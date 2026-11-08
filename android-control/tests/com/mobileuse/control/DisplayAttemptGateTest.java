package com.mobileuse.vnext;

/** Pure JVM checks for the display dispatch gate used by ControlService. */
public final class DisplayAttemptGateTest {
    public static void main(String[] args) {
        cancelBeforeDispatchBlocksCreate();
        dispatchIsSingleShot();
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

    private static void check(boolean condition, String message) {
        if (!condition) {
            throw new AssertionError(message);
        }
    }
}
