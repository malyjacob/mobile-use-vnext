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
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Unique logical owner of the current managed display plus independent retired attempts.
 * A retired attempt never blocks a new create and can never become the current descriptor.
 * CREATE/VERIFY dispatch is one-way; the per-attempt root helper executes system work and
 * reports back by token. STOP only invalidates ownership and requests exact termination.
 */
final class VirtualDisplayHost implements AutoCloseable {
    interface Listener { void onDisplayChanged(String error); }
    interface Completion { void complete(String error); }

    private static final int MAX_RETIRED = 8;
    private static final int CLEANUP_THREADS = 2;
    private static final int CLEANUP_QUEUE_CAPACITY = 6;
    private static final int MAX_RELEASED_CALLBACKS = 3;

    private final DisplayManager manager;
    private final Handler main;
    private final Listener listener;
    private final String appInstance;
    private final HandlerThread frames = new HandlerThread("control-display-frames");
    private final AtomicInteger cleanupThreadSequence = new AtomicInteger();
    // Two workers plus six queued slots accept at most eight tasks. execute() uses
    // the bounded queue's non-blocking offer and throws when all capacity is in use.
    private final ExecutorService cleanup = new ThreadPoolExecutor(
            CLEANUP_THREADS, CLEANUP_THREADS, 0L, TimeUnit.MILLISECONDS,
            new ArrayBlockingQueue<Runnable>(CLEANUP_QUEUE_CAPACITY),
            task -> new Thread(task, "control-display-release-"
                    + cleanupThreadSequence.incrementAndGet()),
            new ThreadPoolExecutor.AbortPolicy());
    private final Object readerLock = new Object();
    private final ArrayList<Attempt> retiring = new ArrayList<>(MAX_RETIRED);
    private final LinkedHashMap<String, Offered> offers = new LinkedHashMap<>();
    private Attempt current;
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

    /** Main-thread command entry. The create command ID is the physical generation. */
    void create(String command, int width, int height, int density, Completion completion) {
        checkCleanup();
        if (closed) throw new IllegalStateException("display_command_closed");
        if (width <= 0 || height <= 0 || density <= 0) {
            throw new IllegalArgumentException("display_dimensions_invalid");
        }
        Attempt existing = current;
        if (existing != null) {
            if (existing.command.equals(command)) {
                if (!existing.ready) throw new IllegalStateException("display_create_in_progress");
                long epoch = existing.verifyEpoch++;
                AtomicBoolean verifyDone = new AtomicBoolean();
                existing.connection.requestVerify(error -> {
                    if (verifyDone.compareAndSet(false, true)) {
                        main.post(() -> finishVerify(existing, epoch, completion, error));
                    }
                });
                main.postDelayed(() -> {
                    if (!verifyDone.compareAndSet(false, true)) return;
                    if (current != existing || existing.verifyEpoch != epoch) {
                        completion.complete("display_create_cancelled");
                    } else {
                        completion.complete("display_verify_timeout");
                        retire(existing, "display_verify_timeout", null);
                    }
                }, RootDisplayProtocol.DEADLINE_MS);
                return;
            }
            // A new exact command supersedes the old logical owner. The old attempt remains
            // in the independent physical cleanup set and cannot publish into the new current.
            retire(existing, null, null);
            checkCleanup();
        }
        for (Attempt old : retiring) {
            if (old.command.equals(command)) {
                throw new IllegalStateException("display_attempt_retired");
            }
        }
        Attempt attempt = new Attempt(command, completion);
        ImageReader reader = ImageReader.newInstance(width, height, PixelFormat.RGBA_8888, 3);
        android.view.Surface allocationSurface;
        try {
            reader.setOnImageAvailableListener(source -> drain(attempt, source),
                    new Handler(frames.getLooper()));
            allocationSurface = reader.getSurface();
        } catch (RuntimeException allocationFailure) {
            reader.close();
            throw allocationFailure;
        }
        attempt.reader = reader;
        current = attempt;
        issue = null;
        listener.onDisplayChanged(null);
        try {
            attempt.connection.requestCreate(allocationSurface, width, height, density,
                    (id, error) -> main.post(() -> finishCreate(attempt, id, error)));
        } catch (RuntimeException requestFailure) {
            current = null;
            closeReader(attempt);
            throw requestFailure;
        }
        Offered bootstrap = offers.remove(command);
        if (bootstrap != null) attempt.connection.attach(bootstrap.endpoint);
        main.postDelayed(() -> {
            if (current == attempt && !attempt.ready) {
                attempt.complete("display_create_timeout");
                retire(attempt, "display_create_timeout", null);
            }
        }, RootDisplayProtocol.DEADLINE_MS);
    }

    /** Bootstrap offers are bounded and keyed by their exact create command identity. */
    void connect(String command, IBinder endpoint) {
        if (command == null || endpoint == null) return;
        if (closed) {
            RootDisplayConnection.closeEndpoint(endpoint, command, appInstance);
        } else if (current != null) {
            if (current.command.equals(command)) current.connection.attach(endpoint);
            else RootDisplayConnection.closeEndpoint(endpoint, command, appInstance);
        } else {
            Offered existing = offers.get(command);
            if (existing != null && existing.endpoint == endpoint) return;
            if (existing != null || offers.size() >= 8) {
                RootDisplayConnection.closeEndpoint(endpoint, command, appInstance);
                return;
            }
            Offered incoming = new Offered(command, endpoint);
            offers.put(command, incoming);
            main.postDelayed(() -> {
                if (offers.remove(command, incoming)) {
                    RootDisplayConnection.closeEndpoint(incoming.endpoint, command, appInstance);
                }
            }, RootDisplayProtocol.DEADLINE_MS);
        }
    }

    private void finishCreate(Attempt attempt, int id, String failure) {
        if (id > 0) attempt.id = id;
        if (current != attempt) {
            if (attempt.cancelled) attempt.complete("display_create_cancelled");
            closeReaderIfCreateSettled(attempt);
            checkCleanup();
            return;
        }
        if (failure != null || id <= 0 || !attempt.connection.helperAlive() || !present(id)) {
            String reason = failure == null ? "display_create_unconfirmed" : failure;
            attempt.complete(reason);
            retire(attempt, reason, null);
            return;
        }
        attempt.ready = true;
        issue = null;
        DiagnosticLog.event(DiagnosticLog.Event.DISPLAY_CREATED, null, "ime_hide_confirmed", -1,
                id, attempt.generation);
        attempt.complete(null);
        listener.onDisplayChanged(null);
    }

    private void finishVerify(Attempt attempt, long epoch, Completion completion, String error) {
        if (current != attempt || attempt.verifyEpoch != epoch) {
            completion.complete("display_create_cancelled");
            return;
        }
        if (error != null) {
            completion.complete(error);
            retire(attempt, error, null);
            return;
        }
        completion.complete(null);
    }

    private void drain(Attempt attempt, ImageReader source) {
        synchronized (readerLock) {
            if (attempt.reader != source) return;
            try (Image image = source.acquireLatestImage()) {
                // Consume frames only; screenshots use the same display's SurfaceFlinger token.
            }
        }
    }

    /** Runtime descriptor for the current attempt only; retired displays never fall back. */
    @SuppressWarnings("deprecation")
    Descriptor descriptor() {
        checkCleanup();
        Attempt candidate = current;
        if (candidate == null) return null;
        if (!candidate.ready) return null;
        if (candidate.id <= 0 || !present(candidate.id)) {
            lost(candidate);
            return null;
        }
        if (current != candidate) return null;
        Display display = manager.getDisplay(candidate.id);
        if (display == null || !display.isValid()) {
            lost(candidate);
            return null;
        }
        DisplayMetrics metrics = new DisplayMetrics();
        display.getRealMetrics(metrics);
        return new Descriptor(candidate.id, metrics.widthPixels, metrics.heightPixels,
                metrics.densityDpi, display.getRotation(), candidate.generation);
    }

    boolean cleanupPending() {
        checkCleanup();
        return !retiring.isEmpty();
    }

    /** Local STOP invalidates current ownership synchronously; physical cleanup is separate. */
    void release() {
        issue = null;
        for (Offered bootstrap : offers.values()) {
            RootDisplayConnection.closeEndpoint(bootstrap.endpoint, bootstrap.command, appInstance);
        }
        offers.clear();
        if (current != null) retire(current, null, null);
        checkCleanup();
    }

    /** Directed close waits for exact helper death/capability release and display absence. */
    void releaseTarget(int displayId, String generation, Completion completion) {
        if (current != null && current.id == displayId && current.generation.equals(generation)) {
            retire(current, null, completion);
            return;
        }
        for (Attempt old : retiring) {
            if (old.id == displayId && old.generation.equals(generation)) {
                addReleased(old, completion);
                checkCleanup();
                return;
            }
        }
        // A different current/pending generation is never a target of this release.
        completion.complete(present(displayId) ? "display_release_identity_unknown" : null);
    }

    /** Cancel only the named create command; success closes its local dispatch authority. */
    void cancelCreate(String targetCommand, Completion completion) {
        if (targetCommand == null || targetCommand.isEmpty()) {
            completion.complete("display_create_target_missing");
            return;
        }
        if (current != null && current.command.equals(targetCommand)) {
            Attempt target = current;
            retire(target, null, null);
            target.complete("display_create_cancelled");
            completion.complete(null);
            return;
        }
        for (Attempt old : retiring) {
            if (old.command.equals(targetCommand)) {
                old.complete("display_create_cancelled");
                completion.complete(null);
                return;
            }
        }
        completion.complete("display_create_target_unknown");
    }

    private void retire(Attempt attempt, String reason, Completion released) {
        boolean wasCurrent = current == attempt;
        if (wasCurrent) current = null;
        addReleased(attempt, released);
        if (!attempt.cancelled) {
            attempt.cancelled = true;
            if (!retiring.contains(attempt) && !attempt.evicted) {
                makeRetiredRoom();
                retiring.add(attempt);
            }
            attempt.connection.cancel();
            if (!attempt.ready) attempt.complete("display_create_cancelled");
        }
        if (wasCurrent && reason != null) {
            issue = reason;
            listener.onDisplayChanged(reason);
        }
        checkCleanup();
    }

    private void checkCleanup() {
        ArrayList<Attempt> complete = new ArrayList<>();
        for (Attempt old : retiring) {
            closeReaderIfCreateSettled(old);
            boolean cleanupConfirmed = old.connection.cleanupConfirmed();
            if (!cleanupConfirmed && !old.connection.helperAlive() && !old.cleanupQueued
                    && !old.cleanupStopped.get()) {
                old.cleanupQueued = true;
                try {
                    cleanup.execute(() -> {
                        if (!old.cleanupStopped.get()) {
                            old.connection.confirmCleanupAfterDeath();
                            main.post(() -> {
                                if (!old.cleanupStopped.get()
                                        && !old.connection.cleanupConfirmed()) {
                                    settleReleased(old, "display_release_unconfirmed");
                                }
                            });
                        }
                    });
                } catch (RejectedExecutionException ignored) {
                    old.cleanupStopped.set(true);
                    settleReleased(old, "display_release_cleanup_stopped");
                    // Capacity stop is terminal for this record; facts stay unconfirmed.
                }
            }
            if (cleanupConfirmed && (old.id <= 0 || !present(old.id))) {
                complete.add(old);
            }
        }
        for (Attempt old : complete) {
            retiring.remove(old);
            if (old.id > 0) {
                DiagnosticLog.event(DiagnosticLog.Event.DISPLAY_RELEASED, null,
                        "display_release_confirmed", -1, old.id, old.generation);
            }
            settleReleased(old, null);
        }
        if (closed && retiring.isEmpty()) cleanup.shutdown();
    }

    private void helperChanged(Attempt attempt) {
        if (current == attempt) {
            if (attempt.ready && !attempt.connection.helperAlive()) {
                lost(attempt);
            } else if (!attempt.ready && !attempt.connection.helperAlive()) {
                attempt.complete("display_helper_died");
                retire(attempt, "display_helper_died", null);
            }
        }
        closeReaderIfCreateSettled(attempt);
        checkCleanup();
    }

    private void lost(Attempt attempt) {
        if (attempt.id > 0) {
            DiagnosticLog.event(DiagnosticLog.Event.DISPLAY_LOST, null, "system_display_lost", -1,
                    attempt.id, attempt.generation);
        }
        retire(attempt, "display_lost", null);
    }

    private boolean present(int id) {
        Display display = id > 0 ? manager.getDisplay(id) : null;
        return display != null && display.isValid();
    }

    private void closeReader(Attempt attempt) {
        synchronized (readerLock) {
            if (attempt.reader == null) return;
            attempt.reader.setOnImageAvailableListener(null, null);
            attempt.reader.close();
            attempt.reader = null;
        }
    }

    private void closeReaderIfCreateSettled(Attempt attempt) {
        if (attempt.connection.createSettled()) closeReader(attempt);
    }

    private void makeRetiredRoom() {
        while (retiring.size() >= MAX_RETIRED) evictOldestRetired();
    }

    private void evictOldestRetired() {
        Attempt evicted = retiring.get(0);
        // Stop any queued/running cleanup from starting new work before exact CLOSE.
        evicted.cleanupStopped.set(true);
        evicted.connection.cancel();
        evicted.evicted = true;
        retiring.remove(0);
        // A dispatched CREATE stays live until its exact result or helper death.
        closeReaderIfCreateSettled(evicted);
        settleReleased(evicted, "display_release_cleanup_stopped");
    }

    private void addReleased(Attempt attempt, Completion completion) {
        if (completion == null) return;
        if (attempt.released.size() >= MAX_RELEASED_CALLBACKS) {
            completion.complete("display_release_callback_limit");
            return;
        }
        attempt.released.add(completion);
    }

    private void settleReleased(Attempt attempt, String error) {
        ArrayList<Completion> released = new ArrayList<>(attempt.released);
        attempt.released.clear();
        for (Completion done : released) done.complete(error);
    }

    @Override public void close() {
        closed = true;
        release();
        manager.unregisterDisplayListener(displayListener);
        frames.quitSafely();
        checkCleanup();
    }

    private final class Attempt {
        final String command;
        final String generation;
        final RootDisplayConnection connection;
        final Completion completion;
        final AtomicBoolean completionOnce = new AtomicBoolean();
        final ArrayList<Completion> released = new ArrayList<>(MAX_RELEASED_CALLBACKS);
        final AtomicBoolean cleanupStopped = new AtomicBoolean();
        ImageReader reader;
        int id = -1;
        boolean ready;
        boolean cancelled;
        boolean evicted;
        boolean cleanupQueued;
        long verifyEpoch;

        Attempt(String commandId, Completion done) {
            command = commandId;
            generation = commandId;
            completion = done;
            connection = new RootDisplayConnection(commandId, appInstance,
                    () -> main.post(() -> helperChanged(this)));
        }

        void complete(String error) {
            if (completionOnce.compareAndSet(false, true)) completion.complete(error);
        }
    }

    private static final class Offered {
        final String command;
        final IBinder endpoint;

        Offered(String id, IBinder binder) {
            command = id;
            endpoint = binder;
        }
    }

    static final class Descriptor {
        final int displayId;
        final int width;
        final int height;
        final int densityDpi;
        final int rotation;
        final String generation;

        Descriptor(int id, int w, int h, int dpi, int r, String epoch) {
            displayId = id;
            width = w;
            height = h;
            densityDpi = dpi;
            rotation = r;
            generation = epoch;
        }

        JSONObject toJson() throws JSONException {
            return new JSONObject().put("display_id", displayId).put("width", width)
                    .put("height", height).put("density_dpi", densityDpi)
                    .put("rotation", rotation).put("generation", generation);
        }
    }
}
