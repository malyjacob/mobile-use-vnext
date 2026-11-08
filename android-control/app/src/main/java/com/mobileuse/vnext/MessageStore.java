package com.mobileuse.vnext;

import android.content.Context;
import android.system.ErrnoException;
import android.system.Os;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/**
 * App 私有的 Agent 消息存储，文件位于 files/messages.json，重启后保留。
 * 只保存消息正文与 App 本地已读状态；已读状态不进入同步请求，也不向服务上报。
 * 上限、去重与字节预算由 MessageList 执行；写入使用同目录临时文件、fsync 与原子 rename，
 * 权限尽量保持 0600。加载或保存失败只记录受控代码，不记录消息正文，也不阻断控制同步或停止。
 */
final class MessageStore {
    static final String FILE_NAME = "messages.json";
    static final String FORMAT = "mobile-use-vnext-app-messages";
    static final int VERSION = 1;
    // 正文上限 1 MiB；JSON 转义最坏会放大数倍，因此文件读取上限留出余量。
    private static final long MAX_FILE_BYTES = 8L * 1024 * 1024;

    private final File file;
    private final MessageList list;
    private long revision;

    private MessageStore(File file, MessageList list) {
        this.file = file;
        this.list = list;
    }

    /** 启动时读取；文件不存在视为空存储，格式或边界错误按空存储继续运行。 */
    static MessageStore load(Context context) {
        File file = new File(context.getFilesDir(), FILE_NAME);
        MessageList list = new MessageList();
        if (!file.exists()) {
            return new MessageStore(file, list);
        }
        try {
            if (file.length() > MAX_FILE_BYTES) {
                throw new IOException("message_file_too_large");
            }
            byte[] body = read(file);
            JSONObject root = new JSONObject(new String(body, StandardCharsets.UTF_8));
            if (!FORMAT.equals(root.getString("format")) || root.getInt("version") != VERSION) {
                throw new IllegalArgumentException("message_file_header");
            }
            JSONArray array = root.getJSONArray("messages");
            if (array.length() > MessageList.MAX_ITEMS) {
                throw new IllegalArgumentException("message_file_count");
            }
            List<MessageList.Item> loaded = new ArrayList<>(array.length());
            Set<String> ids = new HashSet<>();
            int bytes = 0;
            for (int i = 0; i < array.length(); i++) {
                JSONObject item = array.getJSONObject(i);
                String id = item.getString("id");
                long seq = item.getLong("seq");
                String text = item.getString("text");
                String createdAt = item.getString("created_at");
                String receivedAt = item.getString("received_at");
                boolean read = item.getBoolean("read");
                if (id.isEmpty() || seq <= 0 || text.isEmpty()
                        || createdAt.isEmpty() || receivedAt.isEmpty() || !ids.add(id)) {
                    throw new IllegalArgumentException("message_file_item");
                }
                bytes += utf8Length(text);
                if (bytes > MessageList.MAX_TEXT_BYTES) {
                    throw new IllegalArgumentException("message_file_bytes");
                }
                loaded.add(new MessageList.Item(id, seq, text, createdAt, read, receivedAt));
            }
            list = MessageList.restored(loaded);
        } catch (IOException | JSONException | RuntimeException failure) {
            DiagnosticLog.event(DiagnosticLog.Event.MESSAGE_STORE_LOAD_FAILED, null,
                    "message_store_load_failed");
            list = new MessageList();
        }
        return new MessageStore(file, list);
    }

    /** 只更新内存；调用方在确认需要持久化后调用 persist。返回真正新增的项目。 */
    List<MessageList.Item> merge(List<MessageList.Item> delivered) {
        List<MessageList.Item> added = list.merge(delivered);
        if (!added.isEmpty()) {
            revision++;
        }
        return added;
    }

    List<MessageList.Item> listNewestFirst() {
        return list.newestFirst();
    }

    MessageList.Item find(String id) {
        return list.find(id);
    }

    int unreadCount() {
        return list.unreadCount();
    }

    int totalCount() {
        return list.size();
    }

    long revision() {
        return revision;
    }

    /** 已读只在本机生效；立即尝试持久化，失败时内存状态仍保留。 */
    boolean markRead(String id) {
        if (!list.markRead(id)) {
            return false;
        }
        revision++;
        persist();
        return true;
    }

    int markAllRead() {
        int changed = list.markAllRead();
        if (changed > 0) {
            revision++;
            persist();
        }
        return changed;
    }

    /**
     * 原子写入：临时文件写完并 fsync 后 rename 覆盖正式文件；失败时保留旧文件。
     * 消息正文只进入文件与界面，不进入 DiagnosticLog。
     */
    synchronized boolean persist() {
        File temp = new File(file.getPath() + ".tmp");
        FileOutputStream output = null;
        try {
            JSONArray array = new JSONArray();
            for (MessageList.Item item : list.oldestFirst()) {
                array.put(new JSONObject().put("id", item.id).put("seq", item.seq)
                        .put("text", item.text).put("created_at", item.createdAt)
                        .put("read", item.read).put("received_at", item.receivedAt));
            }
            JSONObject root = new JSONObject().put("format", FORMAT)
                    .put("version", VERSION).put("messages", array);
            byte[] body = root.toString().getBytes(StandardCharsets.UTF_8);
            output = new FileOutputStream(temp);
            output.write(body);
            output.flush();
            output.getFD().sync();
            output.close();
            output = null;
            chmod(temp);
            Os.rename(temp.getAbsolutePath(), file.getAbsolutePath());
            chmod(file);
            return true;
        } catch (IOException | JSONException | ErrnoException | RuntimeException failure) {
            DiagnosticLog.event(DiagnosticLog.Event.MESSAGE_STORE_SAVE_FAILED, null,
                    "message_store_save_failed");
            return false;
        } finally {
            if (output != null) {
                try {
                    output.close();
                } catch (IOException ignored) {
                    // 临时文件清理在下方完成。
                }
            }
            if (temp.exists()) {
                // 只删除本次未完成的临时文件，正式文件保持旧内容。
                temp.delete();
            }
        }
    }

    private static byte[] read(File file) throws IOException {
        byte[] body = new byte[(int) file.length()];
        try (FileInputStream input = new FileInputStream(file)) {
            int offset = 0;
            while (offset < body.length) {
                int count = input.read(body, offset, body.length - offset);
                if (count < 0) {
                    throw new IOException("message_file_short_read");
                }
                offset += count;
            }
        }
        return body;
    }

    private static void chmod(File target) {
        try {
            Os.chmod(target.getAbsolutePath(), 0600);
        } catch (ErrnoException ignored) {
            // 私有目录通常已限制访问；权限设置失败不影响消息可用性。
        }
    }

    private static int utf8Length(String text) {
        return text.getBytes(StandardCharsets.UTF_8).length;
    }

    /** 供 ControlService 生成收到时间；存储层不解析服务时间。 */
    static String receivedAtNow() {
        return Instant.now().toString();
    }
}
