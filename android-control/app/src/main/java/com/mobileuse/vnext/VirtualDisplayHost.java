package com.mobileuse.vnext;

import android.content.Context;
import android.graphics.PixelFormat;
import android.hardware.display.DisplayManager;
import android.media.Image;
import android.media.ImageReader;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.IBinder;
import android.util.DisplayMetrics;
import android.view.Display;

import org.json.JSONException;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Unique logical owner of the managed display, Surface and cleanup. A per-attempt root
 * helper performs privileged creation; its callback death token follows the helper AND
 * the App lifetime. HIDE is confirmed before publishing readiness. Main-thread state is
 * separated from blocking allocation and independent cancellation; STOP never waits for
 * a create, root process, system Binder reply or a device lock.
 */
final class VirtualDisplayHost implements AutoCloseable {
    interface Listener { void onDisplayChanged(String error); }
    interface Completion { void complete(String error); }
    private final DisplayManager manager;
    private final Handler main;
    private final Listener listener;
    private final String appInstance;
    private final HandlerThread frames = new HandlerThread("control-display-frames");
    private final ExecutorService allocations = Executors.newSingleThreadExecutor(
            task -> new Thread(task, "control-display-create"));
    private final ExecutorService cleanup = Executors.newCachedThreadPool(
            task -> new Thread(task, "control-display-release"));
    private final Object readerLock = new Object();
    private final ArrayList<Attempt> retiring = new ArrayList<>();
    private Attempt current;
    private final LinkedHashMap<String, Offered> offers = new LinkedHashMap<>();
    private boolean closed;
    private String issue;

    private final DisplayManager.DisplayListener displayListener = new DisplayManager.DisplayListener() {
        @Override public void onDisplayAdded(int id) { }
        @Override public void onDisplayChanged(int id) {
            if (current != null && current.id == id && current.ready) listener.onDisplayChanged(null);
        }
        @Override public void onDisplayRemoved(int id) {
            if (current != null && current.id == id) lost(current);
            checkCleanup();
        }
    };

    VirtualDisplayHost(Context context, Handler handler, String instance, Listener changed) {
        manager = context.getSystemService(DisplayManager.class);
        main = handler;
        appInstance = instance;
        listener = changed;
        frames.start();
        manager.registerDisplayListener(displayListener, main);
    }

    /** Main-thread command entry. The original command ID binds bootstrap, callbacks and
     * completion; neither a timed-out nor a stopped attempt can publish a late descriptor. */
    void create(String command, int width, int height, int density, Completion completion) {
        checkCleanup();
        if (closed || !retiring.isEmpty()) throw new IllegalStateException("display_cleanup_unconfirmed");
        if (current != null) {
            // Never allocate a second screen. Read-only policy revalidation is delegated
            // to the existing helper rather than trusting an old success flag.
            if (!current.ready) throw new IllegalStateException("display_create_in_progress");
            Attempt existing = current;
            allocations.execute(() -> {
                String failure = null;
                try { existing.connection.verify(); }
                catch (IllegalStateException error) { failure = error.getMessage(); }
                String result = failure;
                main.post(() -> {
                    if (current != existing) completion.complete("display_create_cancelled");
                    else if (result != null) {
                        retire(existing, result, () -> completion.complete(result));
                    } else completion.complete(null);
                });
            });
            return;
        }
        if (width <= 0 || height <= 0 || density <= 0) {
            throw new IllegalArgumentException("display_dimensions_invalid");
        }
        Attempt attempt = new Attempt(command, completion);
        ImageReader reader = ImageReader.newInstance(width, height, PixelFormat.RGBA_8888, 3);
        android.view.Surface allocationSurface;
        try {
            reader.setOnImageAvailableListener(source -> drain(attempt, source), new Handler(frames.getLooper()));
            allocationSurface = reader.getSurface();
        } catch (RuntimeException allocationFailure) {
            reader.close();
            throw allocationFailure;
        }
        attempt.reader = reader;
        current = attempt;
        Offered bootstrap = offers.remove(command);
        if (bootstrap != null) attempt.connection.attach(bootstrap.endpoint);
        main.postDelayed(() -> {
            if (current == attempt && !attempt.ready) {
                retire(attempt, "display_create_timeout", () -> completion.complete("display_create_timeout"));
            }
        }, RootDisplayProtocol.DEADLINE_MS);
        allocations.execute(() -> {
            int id = -1;
            String failure = null;
            try { id = attempt.connection.create(allocationSurface, width, height, density); }
            catch (IllegalStateException error) { failure = error.getMessage(); }
            int allocated = id;
            String result = failure;
            main.post(() -> finishCreate(attempt, allocated, result));
        });
    }

    /** Bootstrap may precede the next heartbeat carrying CREATE. Bounded, expiring offers
     * are keyed by command: a late old bootstrap must never evict/close a new one's offer. */
    void connect(String command, IBinder endpoint) {
        if (command == null || endpoint == null) return;
        if (closed) {
            java.util.concurrent.CompletableFuture.runAsync(() ->
                    RootDisplayConnection.closeEndpoint(endpoint, command, appInstance));
        } else if (current != null) {
            if (current.command.equals(command)) current.connection.attach(endpoint);
            else closeOffered(new Offered(command, endpoint));
        } else {
            Offered existing = offers.get(command);
            if (existing != null && existing.endpoint == endpoint) return;
            if (existing != null || offers.size() >= 8) {
                closeOffered(new Offered(command, endpoint));
                return;
            }
            Offered incoming = new Offered(command, endpoint);
            offers.put(command, incoming);
            main.postDelayed(() -> {
                if (offers.remove(command, incoming)) closeOffered(incoming);
            }, RootDisplayProtocol.DEADLINE_MS);
        }
    }

    private void finishCreate(Attempt attempt, int id, String failure) {
        if (id > 0) attempt.id = id;
        if (current != attempt) { checkCleanup(); return; }
        if (failure != null || id <= 0 || !attempt.connection.helperAlive() || !present(id)) {
            String reason = failure == null ? "display_create_unconfirmed" : failure;
            retire(attempt, reason, () -> attempt.completion.complete(reason));
            return;
        }
        attempt.ready = true;
        issue = null;
        DiagnosticLog.event(DiagnosticLog.Event.DISPLAY_CREATED, null, "ime_hide_confirmed", -1,
                id, attempt.generation);
        attempt.completion.complete(null);
        listener.onDisplayChanged(null);
    }

    private void drain(Attempt attempt, ImageReader source) {
        synchronized (readerLock) {
            if (attempt.reader != source) return;
            try (Image image = source.acquireLatestImage()) {
                // Consume frames only; normal screenshots still use the same display's SF token.
            }
        }
    }

    /** Pure runtime descriptor. A known retiring screen remains visible until actual removal,
     * so a failed cleanup cannot masquerade as physical release merely by clearing a reference. */
    @SuppressWarnings("deprecation")
    Descriptor descriptor() {
        checkCleanup();
        Attempt candidate = current != null && current.ready ? current : null;
        if (candidate != null && !present(candidate.id)) { lost(candidate); candidate = null; }
        if (candidate == null) {
            for (Attempt old : retiring) if (old.id > 0 && present(old.id)) { candidate = old; break; }
        }
        if (candidate == null) return null;
        Display display = manager.getDisplay(candidate.id);
        if (display == null || !display.isValid()) {
            if (current == candidate) lost(candidate);
            checkCleanup();
            return null;
        }
        DisplayMetrics metrics = new DisplayMetrics();
        display.getRealMetrics(metrics);
        return new Descriptor(candidate.id, metrics.widthPixels, metrics.heightPixels,
                metrics.densityDpi, display.getRotation(), candidate.generation);
    }

    boolean cleanupPending() { checkCleanup(); return !retiring.isEmpty(); }

    /** Local STOP invalidates ownership synchronously; physical cleanup is independent. */
    void release() {
        for (Offered bootstrap : offers.values()) closeOffered(bootstrap);
        offers.clear();
        issue = null;
        if (current != null) retire(current, null, null);
        checkCleanup();
    }

    /** Directed command confirmation must wait for both death-token and physical absence. */
    void releaseTarget(int displayId, String generation, Completion completion) {
        if (current != null && current.id == displayId && current.generation.equals(generation)) {
            retire(current, null, () -> completion.complete(null));
            return;
        }
        for (Attempt old : retiring) {
            if (old.id == displayId && old.generation.equals(generation)) {
                old.released.add(() -> completion.complete(null));
                checkCleanup();
                return;
            }
        }
        // A different current/pending generation is never a target of this release.
        completion.complete(present(displayId) ? "display_release_identity_unknown" : null);
    }

    private void retire(Attempt attempt, String reason, Runnable released) {
        if (current == attempt) current = null;
        if (released != null) attempt.released.add(released);
        if (!retiring.contains(attempt)) {
            retiring.add(attempt);
            attempt.connection.markCancelled();
            synchronized (readerLock) {
                if (attempt.reader != null) {
                    attempt.reader.setOnImageAvailableListener(null, null);
                    attempt.reader.close();
                    attempt.reader = null;
                }
            }
            cleanup.execute(attempt.connection::cancel);
            if (reason != null) { issue = reason; listener.onDisplayChanged(reason); }
        }
        checkCleanup();
    }

    private void checkCleanup() {
        ArrayList<Attempt> complete = new ArrayList<>();
        for (Attempt old : retiring) {
            if (!old.connection.helperAlive()) cleanup.execute(old.connection::confirmCleanupAfterDeath);
            if (old.connection.cleanupConfirmed()
                    && (old.id <= 0 || !present(old.id))) complete.add(old);
        }
        for (Attempt old : complete) {
            retiring.remove(old);
            if (old.id > 0) DiagnosticLog.event(DiagnosticLog.Event.DISPLAY_RELEASED, null,
                    "display_release_confirmed", -1, old.id, old.generation);
            for (Runnable done : old.released) done.run();
            old.released.clear();
        }
        if (closed && retiring.isEmpty()) cleanup.shutdown();
    }

    private void helperChanged(Attempt attempt) {
        if (current == attempt && attempt.ready && !attempt.connection.helperAlive()) lost(attempt);
        checkCleanup();
        if (!closed) listener.onDisplayChanged(issue);
    }

    private void lost(Attempt attempt) {
        DiagnosticLog.event(DiagnosticLog.Event.DISPLAY_LOST, null, "system_display_lost", -1,
                attempt.id, attempt.generation);
        retire(attempt, "display_lost", null);
    }

    private boolean present(int id) {
        Display display = id > 0 ? manager.getDisplay(id) : null;
        return display != null && display.isValid();
    }

    private void closeOffered(Offered bootstrap) {
        cleanup.execute(() -> RootDisplayConnection.closeEndpoint(
                bootstrap.endpoint, bootstrap.command, appInstance));
    }

    @Override public void close() {
        release();
        closed = true;
        manager.unregisterDisplayListener(displayListener);
        frames.quitSafely();
        allocations.shutdown();
        // Death callbacks may still enqueue the necessary post-death release barrier.
        // Shut the cleanup pool down only after every retiring attempt is confirmed.
        checkCleanup();
    }

    private final class Attempt {
        final String command;
        final String generation = UUID.randomUUID().toString();
        final RootDisplayConnection connection;
        final Completion completion;
        final ArrayList<Runnable> released = new ArrayList<>();
        ImageReader reader;
        int id = -1;
        boolean ready;
        Attempt(String id, Completion done) {
            command = id;
            completion = done;
            connection = new RootDisplayConnection(id, appInstance,
                    () -> main.post(() -> helperChanged(this)));
        }
    }
    private static final class Offered {
        final String command;
        final IBinder endpoint;
        Offered(String id, IBinder binder) { command = id; endpoint = binder; }
    }

    static final class Descriptor {
        final int displayId;
        final int width;
        final int height;
        final int densityDpi;
        final int rotation;
        final String generation;
        Descriptor(int id, int w, int h, int dpi, int r, String epoch) {
            displayId = id; width = w; height = h; densityDpi = dpi; rotation = r; generation = epoch;
        }
        JSONObject toJson() throws JSONException {
            return new JSONObject().put("display_id", displayId).put("width", width)
                    .put("height", height).put("density_dpi", densityDpi)
                    .put("rotation", rotation).put("generation", generation);
        }
    }
}
