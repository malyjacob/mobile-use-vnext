package com.mobileuse.vnext;

import android.os.Binder;
import android.os.IBinder;
import android.os.Parcel;
import android.os.RemoteException;
import android.view.Surface;

import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * A single attempt's capability connection. Ordinary App code uses public Binder/Parcel
 * APIs only. Blocking create/verification runs on the allocation worker; cancellation
 * uses a different worker and never queues behind allocation or the STOP HTTP thread.
 */
final class RootDisplayConnection {
    interface Listener { void onHelperChanged(); }
    private final String commandId;
    private final String appInstance;
    private final Listener listener;
    private final CompletableFuture<IBinder> attached = new CompletableFuture<>();
    private final DisplayAttemptGate gate = new DisplayAttemptGate();
    private final Binder lifetime = new Binder();
    private volatile IBinder endpoint;
    private volatile Capability capability;
    private volatile boolean finished;
    private volatile int displayId = -1;
    private final AtomicBoolean deathCleanupStarted = new AtomicBoolean();
    private volatile boolean deathCleanupConfirmed;
    private final Binder events = new Binder() {
        @Override protected boolean onTransact(int code, Parcel data, Parcel reply, int flags)
                throws RemoteException {
            // AMS cannot authenticate the root sender on the App main thread. This direct
            // callback can: a shell/App impersonator cannot acquire root's Binder identity.
            if (Binder.getCallingUid() != 0) throw new SecurityException("display_root_required");
            data.enforceInterface(RootDisplayProtocol.EVENTS);
            if (!commandId.equals(data.readString()) || !appInstance.equals(data.readString())) {
                throw new SecurityException("display_attempt_mismatch");
            }
            if (code == RootDisplayProtocol.CAPABILITY) {
                IBinder service = data.readStrongBinder();
                IBinder token = data.readStrongBinder();
                int transaction = data.readInt();
                if (service == null || token == null || transaction <= 0 || capability != null) {
                    throw new IllegalArgumentException("display_capability_rejected");
                }
                capability = new Capability(service, token, transaction);
                if (reply != null) reply.writeNoException();
                listener.onHelperChanged();
                return true;
            }
            if (code == RootDisplayProtocol.STOPPED) {
                listener.onHelperChanged();
                return true;
            }
            return super.onTransact(code, data, reply, flags);
        }
    };

    RootDisplayConnection(String command, String instance, Listener changed) {
        commandId = command;
        appInstance = instance;
        listener = changed;
    }

    void attach(IBinder root) {
        if (root == null) return;
        if (endpoint == root) return;
        if (gate.cancelled() || endpoint != null) {
            // Never execute an outgoing Binder call on the App main thread.
            CompletableFuture.runAsync(() -> closeEndpoint(root, commandId, appInstance));
            return;
        }
        endpoint = root;
        try {
            root.linkToDeath(listener::onHelperChanged, 0);
            attached.complete(root);
        } catch (RemoteException dead) {
            attached.completeExceptionally(new IllegalStateException("display_helper_died"));
        }
    }

    int create(Surface surface, int width, int height, int density) {
        Parcel request = Parcel.obtain();
        Parcel response = Parcel.obtain();
        try {
            IBinder root = attached.get(6, TimeUnit.SECONDS);
            if (gate.cancelled()) throw new IllegalStateException("display_create_cancelled");
            writeIdentity(request, commandId, appInstance);
            request.writeStrongBinder(lifetime);
            request.writeStrongBinder(events);
            request.writeTypedObject(surface, 0);
            request.writeInt(width);
            request.writeInt(height);
            request.writeInt(density);
            if (!gate.tryDispatch()) throw new IllegalStateException("display_create_cancelled");
            if (!root.transact(RootDisplayProtocol.CREATE, request, response, 0)) {
                throw new IllegalStateException("display_create_not_supported");
            }
            response.readException();
            int id = response.readInt();
            int policy = response.readInt();
            if (id <= 0 || policy != RootDisplayProtocol.IME_HIDE || capability == null || gate.cancelled()) {
                throw new IllegalStateException("display_ime_hide_unconfirmed");
            }
            displayId = id;
            return id;
        } catch (java.util.concurrent.ExecutionException | java.util.concurrent.TimeoutException
                | RemoteException error) {
            throw new IllegalStateException("display_helper_unavailable");
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("display_create_interrupted");
        } finally {
            finished = true;
            request.recycle();
            response.recycle();
            listener.onHelperChanged();
        }
    }

    /** Called from the independent cleanup executor. Sending CLOSE precedes release so a
     * blocked/late create cannot outrun a token release that happened before allocation. */
    void markCancelled() {
        gate.cancel();
        attached.completeExceptionally(new IllegalStateException("display_create_cancelled"));
    }

    void verify() {
        IBinder root = endpoint;
        if (root == null || gate.cancelled()) throw new IllegalStateException("display_helper_unavailable");
        Parcel request = Parcel.obtain();
        Parcel response = Parcel.obtain();
        try {
            writeIdentity(request, commandId, appInstance);
            if (!root.transact(RootDisplayProtocol.VERIFY, request, response, 0)) {
                throw new IllegalStateException("display_policy_check_unavailable");
            }
            response.readException();
            if (response.readInt() != RootDisplayProtocol.IME_HIDE || gate.cancelled()) {
                throw new IllegalStateException("display_ime_hide_unconfirmed");
            }
        } catch (RemoteException error) {
            throw new IllegalStateException("display_policy_check_unavailable");
        } finally {
            request.recycle();
            response.recycle();
        }
    }

    void cancel() {
        markCancelled();
        IBinder root = endpoint;
        if (root != null) closeEndpoint(root, commandId, appInstance);
        Capability cap = capability;
        if (cap != null) cap.release();
        listener.onHelperChanged();
    }

    boolean cleanupConfirmed() {
        IBinder root = endpoint;
        // No CREATE was dispatched: a delayed bootstrap can only time out/receive CLOSE,
        // never allocate a screen. Otherwise helper death is the late-create barrier.
        return gate.cancelledBeforeDispatch() || (finished && !gate.dispatched())
                || (root != null && !root.isBinderAlive() && deathCleanupConfirmed);
    }

    /** Post-death release ACK is the cleanup barrier even when HIDE failed before the
     * allocation ID could reach the App. An earlier release might race ahead of CREATE;
     * after callback death, any later CREATE must fail its system linkToDeath and clean up.
     * This runs on the independent cleanup pool, never the App/STOP main thread. */
    void confirmCleanupAfterDeath() {
        IBinder root = endpoint;
        if (root == null || root.isBinderAlive() || !deathCleanupStarted.compareAndSet(false, true)) return;
        Capability cap = capability;
        // DMS creation is allowed only after the synchronous CAPABILITY ACK. If it was
        // never delivered then this dead helper could not have dispatched CREATE at all.
        deathCleanupConfirmed = cap == null || cap.release();
        listener.onHelperChanged();
    }

    boolean helperAlive() { return endpoint != null && endpoint.isBinderAlive(); }
    int allocatedId() { return displayId; }

    static void writeIdentity(Parcel data, String command, String instance) {
        data.writeInterfaceToken(RootDisplayProtocol.DESCRIPTOR);
        data.writeString(command);
        data.writeString(instance);
    }

    static void closeEndpoint(IBinder root, String command, String instance) {
        Parcel data = Parcel.obtain();
        try {
            writeIdentity(data, command, instance);
            root.transact(RootDisplayProtocol.CLOSE, data, null, IBinder.FLAG_ONEWAY);
        } catch (RemoteException dead) {
            // Binder death is the cleanup barrier, not a reason to replay creation.
        } finally {
            data.recycle();
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
                // API-36 DMS implements this capability-token release without a caller UID
                // permission check. The transaction comes from that ROM's actual Stub,
                // not a hard-coded or truncated AIDL proxy. No arbitrary call is exposed.
                request.writeInterfaceToken("android.hardware.display.IDisplayManager");
                request.writeStrongBinder(token);
                if (!service.transact(releaseTransaction, request, response, 0)) {
                    throw new IllegalStateException("display_release_not_supported");
                }
                response.readException();
                return true;
            } catch (RemoteException | RuntimeException uncertain) {
                // Do not claim release from this call. The caller independently requires
                // helper death and (when known) actual display absence before confirmation.
                return false;
            } finally {
                request.recycle();
                response.recycle();
            }
        }
    }
}
