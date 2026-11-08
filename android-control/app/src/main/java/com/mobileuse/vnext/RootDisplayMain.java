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
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * One root app_process per display attempt, launched only by the authenticated service's
 * fixed ADB implementation. The helper's callback Binder is the system's display death
 * token. App death, helper death or its deadline therefore also tears down an in-flight
 * create; an App-owned callback alone would leave a late create alive after helper death.
 * Hidden framework calls are confined to this root entry point, never the ordinary App.
 */
public final class RootDisplayMain {
    private final String commandId;
    private final String appInstance;
    private final int appUid;
    private final Handler main;
    private final AtomicBoolean started = new AtomicBoolean();
    private final AtomicBoolean closed = new AtomicBoolean();
    private IBinder events;
    private IBinder owner;
    private volatile int ownedDisplayId = -1;
    private final Runnable deadline = () -> exit();
    private final IBinder.DeathRecipient ownerDied = () -> exit();
    // This node must belong to this process, not the App. Never reuse it for another create.
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
                    main.post(() -> exit());
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
                // Independent Binder thread/main looper; never wait for CREATE's DMS/WMS calls.
                closed.set(true);
                main.post(() -> exit());
                return true;
            }
            if (code == RootDisplayProtocol.VERIFY && !closed.get() && ownedDisplayId > 0) {
                long identity = Binder.clearCallingIdentity();
                try {
                    reply.writeNoException();
                    reply.writeInt(imePolicy(ownedDisplayId));
                } catch (ReflectiveOperationException error) {
                    throw new IllegalStateException("display_policy_check_unavailable");
                } finally { Binder.restoreCallingIdentity(identity); }
                return true;
            }
            if (code != RootDisplayProtocol.CREATE || closed.get() || !started.compareAndSet(false, true)) {
                throw new IllegalStateException("display_create_not_available");
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
                throw new IllegalArgumentException("display_arguments_rejected");
            }
            owner = lifetime;
            events = observer;
            lifetime.linkToDeath(ownerDied, 0);
            // Outgoing system calls and authentication callbacks must carry root's identity,
            // not the App identity inherited from this incoming Binder transaction.
            long identity = Binder.clearCallingIdentity();
            boolean success = false;
            try {
                int id = create(surface, width, height, density);
                if (closed.get() || !lifetime.isBinderAlive()) throw new IllegalStateException("display_cancelled");
                ownedDisplayId = id;
                main.removeCallbacks(deadline);
                reply.writeNoException();
                reply.writeInt(id);
                reply.writeInt(RootDisplayProtocol.IME_HIDE);
                success = true;
            } catch (ReflectiveOperationException | RuntimeException error) {
                // Never serialize framework exception strings or shell output into App diagnostics.
                reply.writeNoException();
                reply.writeInt(-1);
                reply.writeInt(-1);
            } finally {
                surface.release();
                Binder.restoreCallingIdentity(identity);
                if (!success) main.post(() -> exit());
            }
            return true;
        }
    };

    private RootDisplayMain(String command, String instance, int uid) {
        commandId = UUID.fromString(command).toString();
        appInstance = UUID.fromString(instance).toString();
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
            // The deadline and CLOSE remain runnable even if AMS bootstrap blocks.
            Looper.loop();
        } catch (RuntimeException error) {
            // This entry point has no user-content logs or global input-method changes.
            System.exit(2);
        }
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

    private int create(Surface surface, int width, int height, int density)
            throws ReflectiveOperationException, RemoteException {
        IBinder displayService = service("display");
        Class<?> displayStub = Class.forName("android.hardware.display.IDisplayManager$Stub");
        Class<?> displayApi = Class.forName("android.hardware.display.IDisplayManager");
        Object dm = displayStub.getMethod("asInterface", IBinder.class).invoke(null, displayService);
        Class<?> callbackType = Class.forName("android.hardware.display.IVirtualDisplayCallback");
        Object callback = Class.forName("android.hardware.display.IVirtualDisplayCallback$Stub")
                .getMethod("asInterface", IBinder.class).invoke(null, displayToken);
        Field release = displayStub.getDeclaredField("TRANSACTION_releaseVirtualDisplay");
        release.setAccessible(true);
        // Give the App only the exact token-release capability before creation can begin.
        Parcel cap = Parcel.obtain();
        Parcel ack = Parcel.obtain();
        try {
            cap.writeInterfaceToken(RootDisplayProtocol.EVENTS);
            cap.writeString(commandId);
            cap.writeString(appInstance);
            cap.writeStrongBinder(displayService);
            cap.writeStrongBinder(displayToken);
            cap.writeInt(release.getInt(null));
            if (!events.transact(RootDisplayProtocol.CAPABILITY, cap, ack, 0)) {
                throw new IllegalStateException("display_owner_unavailable");
            }
            ack.readException();
        } finally {
            cap.recycle();
            ack.recycle();
        }
        if (closed.get() || !owner.isBinderAlive()) throw new IllegalStateException("display_cancelled");
        VirtualDisplayConfig config = new VirtualDisplayConfig.Builder("MobileUseAgent", width, height, density)
                .setFlags(1 | 8 | (1 << 8) | (1 << 10))
                // PUBLIC | OWN_CONTENT_ONLY | DESTROY_CONTENT_ON_REMOVAL | TRUSTED (API 36).
                .setSurface(surface).build();
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
        if (policy != RootDisplayProtocol.IME_HIDE) throw new IllegalStateException("display_ime_hide_unconfirmed");
        return id;
    }

    private static int imePolicy(int id) throws ReflectiveOperationException {
        Object wm = Class.forName("android.view.IWindowManager$Stub")
                .getMethod("asInterface", IBinder.class).invoke(null, service("window"));
        return (Integer) Class.forName("android.view.IWindowManager")
                .getMethod("getDisplayImePolicy", int.class).invoke(wm, id);
    }

    private void notifyStopped() {
        if (events == null) return;
        long identity = Binder.clearCallingIdentity();
        Parcel data = Parcel.obtain();
        try {
            data.writeInterfaceToken(RootDisplayProtocol.EVENTS);
            data.writeString(commandId);
            data.writeString(appInstance);
            events.transact(RootDisplayProtocol.STOPPED, data, null, IBinder.FLAG_ONEWAY);
        } catch (RemoteException ignored) {
            // Owner death is independently linked to helper exit; no action is replayed.
        } finally {
            data.recycle();
            Binder.restoreCallingIdentity(identity);
        }
    }

    private void exit() {
        closed.set(true);
        // Do not wait on DMS/WMS here. Kernel death of displayToken cleans even a late create.
        Process.killProcess(Process.myPid());
        System.exit(0);
    }
}
