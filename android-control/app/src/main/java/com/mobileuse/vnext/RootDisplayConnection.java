package com.mobileuse.vnext;

import android.os.Binder;
import android.os.IBinder;
import android.os.Parcel;
import android.os.RemoteException;
import android.view.Surface;

import java.util.concurrent.atomic.AtomicBoolean;

/**
 * One display attempt's authenticated helper connection. CREATE and VERIFY are one-way
 * requests whose results return through a root-only event Binder. The App never waits on
 * a remote system transaction, and local cancellation keeps the resource window open
 * until the helper reports a terminal result or Binder death.
 */
final class RootDisplayConnection {
    interface Listener { void onHelperChanged(); }
    interface CreateCompletion { void complete(int displayId, String error); }
    interface OperationCompletion { void complete(String error); }

    private final String commandId;
    private final String appInstance;
    private final Listener listener;
    private final Binder lifetime = new Binder();
    private final DisplayAttemptGate gate = new DisplayAttemptGate();
    private final Object stateLock = new Object();
    private final AtomicBoolean deathCleanupStarted = new AtomicBoolean();
    private final IBinder.DeathRecipient helperDied = this::onHelperDied;
    private final Binder events = new Binder() {
        @Override protected boolean onTransact(int code, Parcel data, Parcel reply, int flags)
                throws RemoteException {
            if (Binder.getCallingUid() != 0) throw new SecurityException("display_root_required");
            data.enforceInterface(RootDisplayProtocol.EVENTS);
            if (!commandId.equals(data.readString()) || !appInstance.equals(data.readString())) {
                throw new SecurityException("display_attempt_mismatch");
            }
            switch (code) {
                case RootDisplayProtocol.CAPABILITY:
                    IBinder service = data.readStrongBinder();
                    IBinder token = data.readStrongBinder();
                    int transaction = data.readInt();
                    if (service == null || token == null || transaction <= 0) {
                        throw new IllegalArgumentException("display_capability_rejected");
                    }
                    synchronized (stateLock) {
                        if (capability != null) {
                            throw new IllegalArgumentException("display_capability_rejected");
                        }
                        capability = new Capability(service, token, transaction);
                    }
                    if (reply != null) reply.writeNoException();
                    listener.onHelperChanged();
                    return true;
                case RootDisplayProtocol.CREATE_RESULT:
                    finishCreateResult(data.readLong(), data.readInt(), data.readInt(), data.readInt());
                    return true;
                case RootDisplayProtocol.VERIFY_RESULT:
                    finishVerifyResult(data.readLong(), data.readInt(), data.readInt());
                    return true;
                case RootDisplayProtocol.STOPPED:
                    synchronized (stateLock) {
                        stopped = true;
                    }
                    listener.onHelperChanged();
                    return true;
                default:
                    return super.onTransact(code, data, reply, flags);
            }
        }
    };

    private volatile IBinder endpoint;
    private volatile Capability capability;
    private PendingCreate createRequest;
    private PendingVerify verifyRequest;
    private boolean rootDead;
    private boolean stopped;
    private boolean deathCleanupConfirmed;
    private long nextRequestToken;
    private int displayId = -1;

    RootDisplayConnection(String command, String instance, Listener changed) {
        commandId = command;
        appInstance = instance;
        listener = changed;
    }

    /** The bootstrap offer may precede CREATE or arrive while CREATE waits asynchronously. */
    void attach(IBinder root) {
        if (root == null) return;
        boolean duplicate = false;
        synchronized (stateLock) {
            if (endpoint == root) return;
            if (gate.cancelled() || endpoint != null) {
                duplicate = true;
            }
        }
        if (duplicate) {
            closeEndpoint(root, commandId, appInstance);
            return;
        }
        IBinder offered = root;
        try {
            offered.linkToDeath(helperDied, 0);
        } catch (RemoteException dead) {
            onHelperDied();
            return;
        }
        PendingCreate pending = null;
        boolean close = false;
        synchronized (stateLock) {
            if (gate.cancelled() || endpoint != null || rootDead) {
                close = true;
            } else {
                endpoint = offered;
                pending = createRequest;
            }
        }
        if (close) {
            closeEndpoint(offered, commandId, appInstance);
            return;
        }
        if (pending != null) dispatchCreate(pending);
        listener.onHelperChanged();
    }

    /** Store a CREATE request and send it when the exact helper offer is available. */
    void requestCreate(Surface surface, int width, int height, int density,
            CreateCompletion completion) {
        if (surface == null || !surface.isValid() || width <= 0 || height <= 0 || density <= 0) {
            throw new IllegalArgumentException("display_arguments_invalid");
        }
        PendingCreate pending;
        synchronized (stateLock) {
            if (gate.cancelled() || gate.dispatched() || createRequest != null) {
                throw new IllegalStateException("display_create_not_available");
            }
            pending = new PendingCreate(++nextRequestToken, surface,
                    width, height, density, completion);
            createRequest = pending;
        }
        IBinder root;
        synchronized (stateLock) {
            root = endpoint;
        }
        if (root != null) dispatchCreate(pending);
    }

    /** Queue a one-way policy verification request for the same helper/attempt. */
    void requestVerify(OperationCompletion completion) {
        PendingVerify pending;
        IBinder root;
        synchronized (stateLock) {
            if (gate.cancelled() || !gate.dispatched() || !gate.settled()
                    || stopped || rootDead || verifyRequest != null) {
                pending = null;
                root = null;
            } else {
                pending = new PendingVerify(++nextRequestToken, completion);
                verifyRequest = pending;
                root = endpoint;
            }
        }
        if (pending == null || root == null || !root.isBinderAlive()) {
            if (pending != null) finishVerify(pending, "display_helper_unavailable");
            else completion.complete("display_helper_unavailable");
            return;
        }
        dispatchVerify(root, pending);
    }

    /** Local ownership invalidation only. The helper-held Surface waits for death. */
    void cancel() {
        PendingCreate cancelledCreate = null;
        PendingVerify cancelledVerify = null;
        IBinder root;
        synchronized (stateLock) {
            gate.cancel();
            if (createRequest != null && !createRequest.sent) {
                cancelledCreate = takeCreateLocked(false);
            }
            cancelledVerify = takeVerifyLocked();
            root = endpoint;
        }
        if (cancelledCreate != null) {
            cancelledCreate.completion.complete(-1, "display_create_cancelled");
        }
        if (cancelledVerify != null) {
            cancelledVerify.completion.complete("display_create_cancelled");
        }
        if (root != null) closeEndpoint(root, commandId, appInstance);
        listener.onHelperChanged();
    }

    private void dispatchCreate(PendingCreate pending) {
        IBinder root;
        Surface surface;
        synchronized (stateLock) {
            if (createRequest != pending || pending.done || pending.sent || gate.cancelled()) return;
            if (!gate.tryDispatch()) {
                pending.sent = true;
                root = null;
                surface = null;
            } else {
                pending.sent = true;
                root = endpoint;
                surface = pending.surface;
            }
        }
        if (root == null || surface == null) {
            finishCreateFailure(pending, "display_create_cancelled", true);
            return;
        }
        if (!root.isBinderAlive()) {
            finishCreateFailure(pending, "display_helper_unavailable", true);
            return;
        }
        Parcel request = Parcel.obtain();
        boolean terminal = false;
        try {
            writeIdentity(request, commandId, appInstance);
            request.writeLong(pending.token);
            request.writeStrongBinder(lifetime);
            request.writeStrongBinder(events);
            request.writeTypedObject(surface, 0);
            request.writeInt(pending.width);
            request.writeInt(pending.height);
            request.writeInt(pending.density);
            if (!root.transact(RootDisplayProtocol.CREATE, request, null, IBinder.FLAG_ONEWAY)) {
                terminal = true;
            }
        } catch (RemoteException dead) {
            terminal = true;
        } catch (RuntimeException unavailable) {
            terminal = false;
        } finally {
            request.recycle();
            synchronized (stateLock) {
                pending.surface = null;
            }
        }
        if (terminal) finishCreateFailure(pending, "display_helper_unavailable", true);
    }

    private void dispatchVerify(IBinder root, PendingVerify pending) {
        Parcel request = Parcel.obtain();
        boolean failed = false;
        try {
            writeIdentity(request, commandId, appInstance);
            request.writeLong(pending.token);
            if (!root.transact(RootDisplayProtocol.VERIFY, request, null, IBinder.FLAG_ONEWAY)) {
                failed = true;
            }
        } catch (RemoteException | RuntimeException unavailable) {
            failed = true;
        } finally {
            request.recycle();
        }
        if (failed) finishVerify(pending, "display_helper_unavailable");
    }

    private void finishCreateResult(long token, int status, int id, int policy) {
        PendingCreate completed;
        String error;
        int resultId = -1;
        synchronized (stateLock) {
            PendingCreate pending = createRequest;
            if (pending == null || pending.done || pending.token != token) return;
            pending.done = true;
            createRequest = null;
            gate.settle();
            completed = pending;
            if (status == RootDisplayProtocol.RESULT_OK && id > 0
                    && policy == RootDisplayProtocol.IME_HIDE && capability != null
                    && !gate.cancelled() && !stopped) {
                displayId = id;
                resultId = id;
                error = null;
            } else {
                error = createError(status == RootDisplayProtocol.RESULT_OK
                        ? RootDisplayProtocol.RESULT_CREATE_FAILED : status);
            }
        }
        completed.completion.complete(resultId, error);
        listener.onHelperChanged();
    }

    private void finishVerifyResult(long token, int status, int policy) {
        PendingVerify completed;
        String error;
        synchronized (stateLock) {
            PendingVerify pending = verifyRequest;
            if (pending == null || pending.token != token) return;
            completed = takeVerifyLocked();
            if (status == RootDisplayProtocol.RESULT_OK
                    && policy == RootDisplayProtocol.IME_HIDE && !gate.cancelled()) {
                error = null;
            } else if (status == RootDisplayProtocol.RESULT_CANCELLED) {
                error = "display_create_cancelled";
            } else if (status == RootDisplayProtocol.RESULT_HELPER_UNAVAILABLE) {
                error = "display_helper_unavailable";
            } else {
                error = "display_ime_hide_unconfirmed";
            }
        }
        completed.completion.complete(error);
        listener.onHelperChanged();
    }

    private void finishCreateFailure(PendingCreate pending, String error, boolean terminal) {
        synchronized (stateLock) {
            if (createRequest != pending || pending.done) return;
            pending.done = true;
            createRequest = null;
            if (terminal) gate.settle();
        }
        pending.completion.complete(-1, error);
        listener.onHelperChanged();
    }

    private void finishVerify(PendingVerify pending, String error) {
        PendingVerify completed;
        synchronized (stateLock) {
            if (verifyRequest != pending) return;
            completed = takeVerifyLocked();
        }
        completed.completion.complete(error);
        listener.onHelperChanged();
    }

    private void onHelperDied() {
        PendingCreate failedCreate;
        PendingVerify failedVerify;
        synchronized (stateLock) {
            rootDead = true;
            stopped = true;
            gate.settleByDeath();
            failedCreate = takeCreateLocked(false);
            failedVerify = takeVerifyLocked();
        }
        if (failedCreate != null) {
            failedCreate.completion.complete(-1, "display_helper_died");
        }
        if (failedVerify != null) {
            failedVerify.completion.complete("display_helper_died");
        }
        listener.onHelperChanged();
    }

    private PendingCreate takeCreateLocked(boolean terminal) {
        PendingCreate pending = createRequest;
        if (pending == null || pending.done) return null;
        pending.done = true;
        createRequest = null;
        if (terminal) gate.settle();
        return pending;
    }

    private PendingVerify takeVerifyLocked() {
        PendingVerify pending = verifyRequest;
        if (pending == null) return null;
        verifyRequest = null;
        return pending;
    }

    boolean cleanupConfirmed() {
        synchronized (stateLock) {
            return gate.cancelledBeforeDispatch()
                    || (rootDead && deathCleanupConfirmed);
        }
    }

    /** Runs on the bounded cleanup worker after Binder death, never on App main. */
    void confirmCleanupAfterDeath() {
        IBinder root;
        Capability cap;
        synchronized (stateLock) {
            root = endpoint;
            cap = capability;
        }
        if ((root == null || root.isBinderAlive()) && !isRootDead()) return;
        synchronized (stateLock) {
            rootDead = true;
        }
        if (!deathCleanupStarted.compareAndSet(false, true)) return;
        boolean confirmed = cap == null || cap.release();
        synchronized (stateLock) {
            deathCleanupConfirmed = confirmed;
        }
        listener.onHelperChanged();
    }

    boolean helperAlive() {
        IBinder root;
        synchronized (stateLock) {
            root = endpoint;
        }
        return root != null && root.isBinderAlive() && !isRootDead();
    }

    /** Reader/callback resources stay held after local cancel until this becomes true. */
    boolean createSettled() {
        synchronized (stateLock) {
            return gate.cancelledBeforeDispatch() || (!gate.dispatched() && rootDead)
                    || (gate.dispatched() && gate.settled());
        }
    }

    int allocatedId() {
        synchronized (stateLock) {
            return displayId;
        }
    }

    private boolean isRootDead() {
        synchronized (stateLock) {
            return rootDead;
        }
    }

    static void writeIdentity(Parcel data, String command, String instance) {
        data.writeInterfaceToken(RootDisplayProtocol.DESCRIPTOR);
        data.writeString(command);
        data.writeString(instance);
    }

    static void closeEndpoint(IBinder root, String command, String instance) {
        if (root == null) return;
        Parcel data = Parcel.obtain();
        try {
            writeIdentity(data, command, instance);
            root.transact(RootDisplayProtocol.CLOSE, data, null, IBinder.FLAG_ONEWAY);
        } catch (RemoteException | RuntimeException dead) {
            // Death is the cleanup barrier; no device action is replayed from this path.
        } finally {
            data.recycle();
        }
    }

    private static String createError(int status) {
        switch (status) {
            case RootDisplayProtocol.RESULT_CANCELLED:
                return "display_create_cancelled";
            case RootDisplayProtocol.RESULT_IME_HIDE_UNCONFIRMED:
                return "display_ime_hide_unconfirmed";
            case RootDisplayProtocol.RESULT_HELPER_UNAVAILABLE:
                return "display_helper_unavailable";
            case RootDisplayProtocol.RESULT_CREATE_FAILED:
            default:
                return "display_create_unconfirmed";
        }
    }

    private static final class PendingCreate {
        final long token;
        final int width;
        final int height;
        final int density;
        final CreateCompletion completion;
        Surface surface;
        boolean sent;
        boolean done;

        PendingCreate(long requestToken, Surface requestSurface, int w, int h, int d,
                CreateCompletion callback) {
            token = requestToken;
            surface = requestSurface;
            width = w;
            height = h;
            density = d;
            completion = callback;
        }
    }

    private static final class PendingVerify {
        final long token;
        final OperationCompletion completion;

        PendingVerify(long requestToken, OperationCompletion callback) {
            token = requestToken;
            completion = callback;
        }
    }

    private static final class Capability {
        final IBinder service;
        final IBinder token;
        final int releaseTransaction;

        Capability(IBinder manager, IBinder callback, int transaction) {
            service = manager;
            token = callback;
            releaseTransaction = transaction;
        }

        boolean release() {
            Parcel request = Parcel.obtain();
            Parcel response = Parcel.obtain();
            try {
                request.writeInterfaceToken("android.hardware.display.IDisplayManager");
                request.writeStrongBinder(token);
                if (!service.transact(releaseTransaction, request, response, 0)) return false;
                response.readException();
                return true;
            } catch (RemoteException | RuntimeException uncertain) {
                return false;
            } finally {
                request.recycle();
                response.recycle();
            }
        }
    }
}
