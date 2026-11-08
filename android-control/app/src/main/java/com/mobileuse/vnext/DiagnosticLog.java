package com.mobileuse.vnext;

import android.content.Context;
import android.os.SystemClock;
import android.system.ErrnoException;
import android.system.Os;
import android.util.Log;

import org.json.JSONException;
import org.json.JSONObject;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.UUID;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.regex.Pattern;

/**
 * 固定事件、原因码、UUID 和数字组成的私有日志，不接收 Throwable、请求、节点或用户文本。
 * 主线程只提交到有界队列；单写线程负责每份 512 KiB、保留一份备份的轮转，不阻塞 STOP。
 * 进程终止只保证已完成的写入；不在退出时重放记录，也不向服务上传原始日志。
 */
final class DiagnosticLog {
    enum Event {
        STARTED("control.started"), STOPPED("control.stopped"),
        CONNECTED("control.connected"), DISCONNECTED("control.disconnected"),
        INSTANCE_CHANGED("control.service_instance_changed"),
        STOP_REQUESTED("control.stop.requested"), STOP_CONFIRMED("control.stop.confirmed"),
        RESPONSE_DISCARDED("control.response.discarded"),
        COMMAND_START("command.start"), COMMAND_END("command.end"),
        DISPLAY_CREATED("display.created"), DISPLAY_RELEASED("display.released"),
        DISPLAY_LOST("display.lost"),
        // 控制视图只记状态枚举与版本，不记节点、正文、命令或原始响应。
        CONTROL_UPDATED("control.view.updated"), CONTRACT_MISMATCH("control.contract_mismatch"),
        STOP_SEQ_ADVANCED("control.stop.seq_advanced"),
        REQUEST_FAILED("request.failed"), REQUEST_RECOVERED("request.recovered");
        final String wire;
        Event(String wire) { this.wire = wire; }
    }

    private static final String TAG = "MobileUseDiagnostics";
    private static final int MAX_BYTES = 512 * 1024;
    private static final Pattern CODE = Pattern.compile("[a-zA-Z_][a-zA-Z0-9_.-]{0,79}");
    private static final byte[] NEWLINE = { '\n' };
    private static final ArrayBlockingQueue<byte[]> pending = new ArrayBlockingQueue<>(256);
    private static String instanceId;
    private static File file;
    private static FileOutputStream output;
    private static long bytes;
    private static long sequence;
    private static long dropped;
    private static long failures;
    private static final String[] networkFailures = new String[2];
    private static final long[] repeatedFailures = new long[2];

    static synchronized void initialize(Context context, String appInstanceId) {
        instanceId = appInstanceId;
        if (file == null) {
            file = new File(context.getFilesDir(), "diagnostics.log");
            new Thread(() -> {
                try {
                    while (true) { append(pending.take()); }
                } catch (InterruptedException stopped) {
                    Thread.currentThread().interrupt();
                }
            }, "control-diagnostics").start();
        }
    }

    static void event(Event event, String objectId, String code) {
        event(event, objectId, code, -1, -1, null);
    }

    static synchronized void event(Event event, String objectId, String code,
            long ms, int displayId, String generation) {
        if (instanceId == null) { return; }
        try {
            JSONObject entry = new JSONObject().put("schema_version", 1)
                    .put("time", Instant.now().toString()).put("level",
                            code == null || event == Event.STOP_REQUESTED || event == Event.REQUEST_RECOVERED
                                    ? "INFO" : "WARNING")
                    .put("event", event.wire).put("component", "control")
                    .put("instance_id", instanceId).put("seq", ++sequence)
                    .put("monotonic_ms", SystemClock.elapsedRealtime())
                    .put("file_dropped", dropped).put("file_errors", failures)
                    .put("repeated_failures", repeatedFailures[0] + repeatedFailures[1]);
            if (objectId != null && objectId.length() == 36) {
                entry.put("object_id", UUID.fromString(objectId).toString());
            }
            if (code != null && CODE.matcher(code).matches()) { entry.put("code", code); }
            if (ms >= 0) { entry.put("ms", ms); }
            if (displayId >= 0) { entry.put("display_id", displayId); }
            if (generation != null) {
                entry.put("display_generation", UUID.fromString(generation).toString());
            }
            if (!pending.offer(entry.toString().getBytes(StandardCharsets.UTF_8))) { dropped++; }
        } catch (JSONException | IllegalArgumentException invalidMetadata) {
            failures++;
        }
    }

    /** 两条 HTTP 线程分别去重；恢复记录包含此前被合并的重复次数。正常同步不写逐次日志。 */
    static synchronized void network(boolean sync, String requestId, String code, long ms) {
        int lane = sync ? 0 : 1;
        String previous = networkFailures[lane];
        if (code != null && code.equals(previous)) {
            repeatedFailures[lane]++;
            return;
        }
        if (code != null || previous != null) {
            event(code == null ? Event.REQUEST_RECOVERED : Event.REQUEST_FAILED,
                    requestId, code == null ? null : (sync ? "sync." : "stop.") + code,
                    ms, -1, null);
        }
        networkFailures[lane] = code;
        repeatedFailures[lane] = 0;
    }

    /** 供响应错误体识别受控 code；不匹配白名单的值不进入事件日志。 */
    static boolean validCode(String code) {
        return code != null && CODE.matcher(code).matches();
    }

    private static void append(byte[] line) {
        try {
            if (output == null) {
                bytes = file.length();
                output = new FileOutputStream(file, true);
                Os.chmod(file.getAbsolutePath(), 0600);
            }
            if (bytes + line.length + 1 > MAX_BYTES) {
                output.close();
                output = null;
                File backup = new File(file.getPath() + ".1");
                if ((backup.exists() && !backup.delete()) || !file.renameTo(backup)) {
                    throw new IOException("diagnostic_rotation_failed");
                }
                output = new FileOutputStream(file);
                Os.chmod(file.getAbsolutePath(), 0600);
                bytes = 0;
            }
            output.write(line);
            output.write(NEWLINE);
            output.flush();
            bytes += line.length + 1;
        } catch (IOException | ErrnoException failure) {
            synchronized (DiagnosticLog.class) {
                if (++failures == 1) {
                    Log.e(TAG, "{\"event\":\"logging.file_failed\",\"component\":\"control\"}");
                }
            }
        }
    }

    private DiagnosticLog() { }
}
