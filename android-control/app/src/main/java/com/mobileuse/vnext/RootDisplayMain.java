package com.mobileuse.vnext;

import android.content.ComponentName;
import android.content.Intent;
import android.hardware.display.VirtualDisplayConfig;
import android.os.Binder;
import android.os.Bundle;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.Parcel;
import android.os.Process;
import android.os.RemoteException;
import android.view.Surface;

import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

/**
 * One root app_process per display attempt. The endpoint accepts one-way CREATE and VERIFY
 * requests and executes them on this helper's single work thread. The helper retains the
 * Surface until CLOSE/death or a terminal create failure, so App-side logical cancellation
 * cannot invalidate a system call that is still in flight.
 */
public final class RootDisplayMain {
    private final String commandId;
    private final String appInstance;
    private final int appUid;
    private final Handler main;
    private final ExecutorService work = Executors.newSingleThreadExecutor(
            task -> new Thread(task, "display-helper-work"));
    private final AtomicBoolean started = new AtomicBoolean();
    private final AtomicBoolean closed = new AtomicBoolean();
    private final AtomicReference<Surface> createSurface = new AtomicReference<>();
    private volatile IBinder events;
    private volatile IBinder owner;
    private volatile int ownedDisplayId = -1;
    private final Runnable deadline = this::exit;
    private final IBinder.DeathRecipient ownerDied = this::exit;

    // This node belongs to the helper process and is also the system display death token.
    private final Binder displayToken = new Binder() {
        @Override protected boolean onTransact(int code, Parcel data, Parcel reply, int flags)
                throws RemoteException {
            if (code == INTERFACE_TRANSACTION) {
                if (reply != null) reply.writeString("android.hardware.display.IVirtualDisplayCallback");
                return true;
            }
            if (code >= 1 && code <= 3) {
                data.enforceInterface("android.hardware.display.IVirtualDisplayCallback");
                if (code == 3) {
                    notifyStopped();
                    main.post(RootDisplayMain.this::exit);
                }
                return true;
            }
            return super.onTransact(code, data, reply, flags);
        }
    };

    private final Binder endpoint = new Binder() {
        @Override protected boolean onTransact(int code, Parcel data, Parcel reply, int flags)
                throws RemoteException {
            if (code == INTERFACE_TRANSACTION) {
                if (reply != null) reply.writeString(RootDisplayProtocol.DESCRIPTOR);
                return true;
            }
            if (Binder.getCallingUid() != appUid) throw new SecurityException("display_caller_rejected");
            data.enforceInterface(RootDisplayProtocol.DESCRIPTOR);
            if (!commandId.equals(data.readString()) || !appInstance.equals(data.readString())) {
                throw new SecurityException("display_identity_rejected");
            }
            if (code == RootDisplayProtocol.CLOSE) {
                closed.set(true);
                main.post(RootDisplayMain.this::exit);
                return true;
            }
            if (code == RootDisplayProtocol.VERIFY) {
                submitVerify(data.readLong());
                return true;
            }
            if (code == RootDisplayProtocol.CREATE) {
                submitCreate(data.readLong(), data);
                return true;
            }
            return super.onTransact(code, data, reply, flags);
        }
    };

    private RootDisplayMain(String command, String instance, int uid) {
        UUID.fromString(command);
        UUID.fromString(instance);
        commandId = command;
        appInstance = instance;
        if (uid < 10000) throw new IllegalArgumentException("display_app_uid_rejected");
        appUid = uid;
        main = new Handler(Looper.getMainLooper());
    }

    public static void main(String[] args) {
        if (Process.myUid() != 0 || args.length != 3) System.exit(2);
        try {
            Looper.prepareMainLooper();
            RootDisplayMain helper = new RootDisplayMain(args[0], args[1], Integer.parseInt(args[2]));
            helper.main.postDelayed(helper.deadline, RootDisplayProtocol.DEADLINE_MS);
            new Thread(() -> {
                try { helper.attach(); }
                catch (ReflectiveOperationException | RuntimeException failure) { helper.exit(); }
            }, "display-helper-attach").start();
            Looper.loop();
        } catch (RuntimeException error) {
            System.exit(2);
        }
    }

    private void submitCreate(long request, Parcel data) {
        if (closed.get() || !started.compareAndSet(false, true)) {
            sendCreateResult(request, RootDisplayProtocol.RESULT_CANCELLED, -1, -1);
            return;
        }
        IBinder lifetime = data.readStrongBinder();
        IBinder observer = data.readStrongBinder();
        Surface surface = data.readTypedObject(Surface.CREATOR);
        int width = data.readInt();
        int height = data.readInt();
        int density = data.readInt();
        if (lifetime == null || observer == null || surface == null || !surface.isValid()
                || width <= 0 || height <= 0 || density <= 0) {
            if (surface != null) surface.release();
            sendCreateResult(request, RootDisplayProtocol.RESULT_CREATE_FAILED, -1, -1);
            main.post(this::exit);
            return;
        }
        owner = lifetime;
        events = observer;
        try {
            lifetime.linkToDeath(ownerDied, 0);
        } catch (RemoteException dead) {
            surface.release();
            main.post(this::exit);
            return;
        }
        if (!createSurface.compareAndSet(null, surface)) {
            surface.release();
            sendCreateResult(request, RootDisplayProtocol.RESULT_CANCELLED, -1, -1);
            main.post(this::exit);
            return;
        }
        try {
            work.execute(() -> runCreate(request, width, height, density));
        } catch (RejectedExecutionException rejected) {
            sendCreateResult(request, RootDisplayProtocol.RESULT_CANCELLED, -1, -1);
            main.post(this::exit);
        }
    }

    private void submitVerify(long request) {
        try {
            work.execute(() -> runVerify(request));
        } catch (RejectedExecutionException rejected) {
            sendVerifyResult(request, RootDisplayProtocol.RESULT_VERIFY_UNAVAILABLE, -1);
        }
    }

    private void runCreate(long request, int width, int height, int density) {
        int status = RootDisplayProtocol.RESULT_CREATE_FAILED;
        int display = -1;
        long identity = Binder.clearCallingIdentity();
        try {
            sendCapability();
            IBinder currentOwner = owner;
            if (closed.get() || currentOwner == null || !currentOwner.isBinderAlive()) {
                status = RootDisplayProtocol.RESULT_CANCELLED;
            } else {
                display = createDisplay(width, height, density);
                if (closed.get() || !currentOwner.isBinderAlive()) {
                    status = RootDisplayProtocol.RESULT_CANCELLED;
                } else {
                    ownedDisplayId = display;
                    main.removeCallbacks(deadline);
                    status = RootDisplayProtocol.RESULT_OK;
                }
            }
        } catch (IllegalStateException failure) {
            if ("display_owner_unavailable".equals(failure.getMessage())) {
                status = RootDisplayProtocol.RESULT_HELPER_UNAVAILABLE;
            } else if ("display_ime_hide_unconfirmed".equals(failure.getMessage())) {
                status = RootDisplayProtocol.RESULT_IME_HIDE_UNCONFIRMED;
            }
        } catch (RemoteException failure) {
            status = RootDisplayProtocol.RESULT_CREATE_FAILED;
        } catch (ReflectiveOperationException | RuntimeException failure) {
            status = RootDisplayProtocol.RESULT_CREATE_FAILED;
        } finally {
            Binder.restoreCallingIdentity(identity);
        }
        boolean delivered = sendCreateResult(request, status,
                status == RootDisplayProtocol.RESULT_OK ? display : -1,
                status == RootDisplayProtocol.RESULT_OK ? RootDisplayProtocol.IME_HIDE : -1);
        if (status != RootDisplayProtocol.RESULT_OK || !delivered) main.post(this::exit);
    }

    private void runVerify(long request) {
        int status;
        int policy = -1;
        long identity = Binder.clearCallingIdentity();
        try {
            if (closed.get() || ownedDisplayId <= 0) {
                status = RootDisplayProtocol.RESULT_VERIFY_UNAVAILABLE;
            } else {
                policy = imePolicy(ownedDisplayId);
                status = RootDisplayProtocol.RESULT_OK;
            }
        } catch (ReflectiveOperationException | RuntimeException failure) {
            status = RootDisplayProtocol.RESULT_VERIFY_UNAVAILABLE;
        } finally {
            Binder.restoreCallingIdentity(identity);
        }
        sendVerifyResult(request, status, policy);
    }

    private static IBinder service(String name) throws ReflectiveOperationException {
        return (IBinder) Class.forName("android.os.ServiceManager")
                .getMethod("getService", String.class).invoke(null, name);
    }

    private void attach() throws ReflectiveOperationException {
        Intent intent = new Intent(RootDisplayProtocol.ACTION_ATTACH)
                .setComponent(new ComponentName("com.mobileuse.vnext", "com.mobileuse.vnext.ControlService"))
                .putExtra(RootDisplayProtocol.EXTRA_COMMAND, commandId)
                .putExtra(RootDisplayProtocol.EXTRA_INSTANCE, appInstance);
        Bundle extras = new Bundle();
        extras.putBinder(RootDisplayProtocol.EXTRA_ENDPOINT, endpoint);
        intent.putExtras(extras);
        Object am = Class.forName("android.app.ActivityManager").getMethod("getService").invoke(null);
        Class<?> caller = Class.forName("android.app.IApplicationThread");
        Class<?> api = Class.forName("android.app.IActivityManager");
        Object result = api.getMethod("startService", caller, Intent.class, String.class, boolean.class,
                String.class, String.class, int.class).invoke(am, null, intent, null, false,
                "com.mobileuse.vnext", null, appUid / 100000);
        if (!(result instanceof ComponentName)
                || !"com.mobileuse.vnext".equals(((ComponentName) result).getPackageName())) {
            throw new IllegalStateException("display_attach_rejected");
        }
    }

    private void sendCapability() {
        IBinder observer = events;
        if (observer == null) throw new IllegalStateException("display_owner_unavailable");
        long identity = Binder.clearCallingIdentity();
        Parcel cap = Parcel.obtain();
        Parcel ack = Parcel.obtain();
        try {
            IBinder displayService = service("display");
            Class<?> displayStub = Class.forName("android.hardware.display.IDisplayManager$Stub");
            Field release = displayStub.getDeclaredField("TRANSACTION_releaseVirtualDisplay");
            release.setAccessible(true);
            cap.writeInterfaceToken(RootDisplayProtocol.EVENTS);
            cap.writeString(commandId);
            cap.writeString(appInstance);
            cap.writeStrongBinder(displayService);
            cap.writeStrongBinder(displayToken);
            cap.writeInt(release.getInt(null));
            if (!observer.transact(RootDisplayProtocol.CAPABILITY, cap, ack, 0)) {
                throw new IllegalStateException("display_owner_unavailable");
            }
            ack.readException();
        } catch (RemoteException | ReflectiveOperationException | RuntimeException failure) {
            throw new IllegalStateException("display_owner_unavailable");
        } finally {
            cap.recycle();
            ack.recycle();
            Binder.restoreCallingIdentity(identity);
        }
    }

    private int createDisplay(int width, int height, int density)
            throws ReflectiveOperationException, RemoteException {
        IBinder displayService = service("display");
        Class<?> displayStub = Class.forName("android.hardware.display.IDisplayManager$Stub");
        Class<?> displayApi = Class.forName("android.hardware.display.IDisplayManager");
        Object dm = displayStub.getMethod("asInterface", IBinder.class).invoke(null, displayService);
        Class<?> callbackType = Class.forName("android.hardware.display.IVirtualDisplayCallback");
        Object callback = Class.forName("android.hardware.display.IVirtualDisplayCallback$Stub")
                .getMethod("asInterface", IBinder.class).invoke(null, displayToken);
        Surface heldSurface = createSurface.get();
        if (heldSurface == null || !heldSurface.isValid()) {
            throw new IllegalStateException("display_create_failed");
        }
        VirtualDisplayConfig config = new VirtualDisplayConfig.Builder(
                "MobileUseAgent", width, height, density)
                .setFlags(1 | 8 | (1 << 8) | (1 << 10))
                // PUBLIC | OWN_CONTENT_ONLY | DESTROY_CONTENT_ON_REMOVAL | TRUSTED (API 36).
                .setSurface(heldSurface).build();
        Method create = displayApi.getMethod("createVirtualDisplay", VirtualDisplayConfig.class,
                callbackType, Class.forName("android.media.projection.IMediaProjection"), String.class);
        int id = (Integer) create.invoke(dm, config, callback, null, "com.mobileuse.vnext");
        if (id <= 0 || closed.get()) throw new IllegalStateException("display_create_failed");
        Object wm = Class.forName("android.view.IWindowManager$Stub")
                .getMethod("asInterface", IBinder.class).invoke(null, service("window"));
        Class<?> windowApi = Class.forName("android.view.IWindowManager");
        windowApi.getMethod("setDisplayImePolicy", int.class, int.class)
                .invoke(wm, id, RootDisplayProtocol.IME_HIDE);
        int policy = (Integer) windowApi.getMethod("getDisplayImePolicy", int.class).invoke(wm, id);
        if (policy != RootDisplayProtocol.IME_HIDE) {
            throw new IllegalStateException("display_ime_hide_unconfirmed");
        }
        return id;
    }

    private static int imePolicy(int id) throws ReflectiveOperationException {
        Object wm = Class.forName("android.view.IWindowManager$Stub")
                .getMethod("asInterface", IBinder.class).invoke(null, service("window"));
        return (Integer) Class.forName("android.view.IWindowManager")
                .getMethod("getDisplayImePolicy", int.class).invoke(wm, id);
    }

    private boolean sendCreateResult(long request, int status, int display, int policy) {
        Parcel data = Parcel.obtain();
        try {
            data.writeInterfaceToken(RootDisplayProtocol.EVENTS);
            data.writeString(commandId);
            data.writeString(appInstance);
            data.writeLong(request);
            data.writeInt(status);
            data.writeInt(display);
            data.writeInt(policy);
            return sendEvent(RootDisplayProtocol.CREATE_RESULT, data);
        } finally {
            data.recycle();
        }
    }

    private void sendVerifyResult(long request, int status, int policy) {
        Parcel data = Parcel.obtain();
        try {
            data.writeInterfaceToken(RootDisplayProtocol.EVENTS);
            data.writeString(commandId);
            data.writeString(appInstance);
            data.writeLong(request);
            data.writeInt(status);
            data.writeInt(policy);
            sendEvent(RootDisplayProtocol.VERIFY_RESULT, data);
        } finally {
            data.recycle();
        }
    }

    private boolean sendEvent(int code, Parcel data) {
        IBinder observer = events;
        if (observer == null) return false;
        long identity = Binder.clearCallingIdentity();
        try {
            return observer.transact(code, data, null, IBinder.FLAG_ONEWAY);
        } catch (RemoteException dead) {
            return false;
        } finally {
            Binder.restoreCallingIdentity(identity);
        }
    }

    private void notifyStopped() {
        IBinder observer = events;
        if (observer == null) return;
        Parcel data = Parcel.obtain();
        try {
            data.writeInterfaceToken(RootDisplayProtocol.EVENTS);
            data.writeString(commandId);
            data.writeString(appInstance);
            sendEvent(RootDisplayProtocol.STOPPED, data);
        } finally {
            data.recycle();
        }
    }

    private void exit() {
        closed.set(true);
        work.shutdownNow();
        // Keep the CREATE Surface owned until process death. A helper exit is not a
        // terminal CREATE result, and App-side cancellation must not recycle it early.
        Process.killProcess(Process.myPid());
        System.exit(0);
    }
}
