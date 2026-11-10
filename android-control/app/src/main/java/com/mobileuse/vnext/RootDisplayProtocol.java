package com.mobileuse.vnext;

/** Fixed, private API-36 display bridge. No arbitrary shell, display ID or Binder call is accepted. */
final class RootDisplayProtocol {
    static final String DESCRIPTOR = "com.mobileuse.vnext.RootDisplay.v2";
    static final String EVENTS = "com.mobileuse.vnext.RootDisplayEvents.v2";
    static final String ACTION_ATTACH = "com.mobileuse.vnext.ATTACH_DISPLAY_HELPER";
    static final String EXTRA_ENDPOINT = "display_helper";
    static final String EXTRA_COMMAND = "display_command";
    static final String EXTRA_INSTANCE = "display_app_instance";
    static final int CREATE = 1;
    static final int CLOSE = 2;
    static final int VERIFY = 3;
    static final int CAPABILITY = 1;
    static final int STOPPED = 2;
    static final int CREATE_RESULT = 3;
    static final int VERIFY_RESULT = 4;
    static final int RESULT_OK = 0;
    static final int RESULT_CANCELLED = 1;
    static final int RESULT_CREATE_FAILED = 2;
    static final int RESULT_IME_HIDE_UNCONFIRMED = 3;
    static final int RESULT_VERIFY_UNAVAILABLE = 4;
    static final int RESULT_HELPER_UNAVAILABLE = 5;
    static final int IME_HIDE = 2;
    static final long DEADLINE_MS = 8000;
    private RootDisplayProtocol() { }
}
