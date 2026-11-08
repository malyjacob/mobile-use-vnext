package com.mobileuse.vnext;

import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/**
 * App 私有消息列表的纯 Java 逻辑：按 id 去重、条数与文本字节上限淘汰、已读状态与排序。
 * 不依赖 Android、文件或网络；MessageStore 负责持久化，ControlService 负责协议合并。
 * 内部始终按 seq 升序（最旧到最新）保存，超限时只从最旧的一端淘汰。
 */
final class MessageList {
    static final int MAX_ITEMS = 256;
    static final int MAX_TEXT_BYTES = 1 << 20;

    static final class Item {
        final String id;
        final long seq;
        final String text;
        final String createdAt;
        final boolean read;
        final String receivedAt;

        Item(String id, long seq, String text, String createdAt, boolean read, String receivedAt) {
            this.id = id;
            this.seq = seq;
            this.text = text;
            this.createdAt = createdAt;
            this.read = read;
            this.receivedAt = receivedAt;
        }

        Item asRead() {
            return read ? this : new Item(id, seq, text, createdAt, true, receivedAt);
        }
    }

    private final ArrayList<Item> items = new ArrayList<>();
    private final Set<String> ids = new HashSet<>();
    private int textBytes;

    /** 从已经通过格式与边界校验的持久化记录恢复；不再做淘汰或去重。 */
    static MessageList restored(List<Item> loaded) {
        MessageList list = new MessageList();
        for (Item item : loaded) {
            if (item == null || item.id == null || list.ids.contains(item.id)) {
                continue;
            }
            list.add(item);
            list.ids.add(item.id);
        }
        list.sort();
        return list;
    }

    /**
     * 合并一次同步收到的消息。同一 id 只保留首次出现的记录，已读状态不回退；
     * 单条文本超过总预算时放弃该条，其余按最旧优先淘汰到上限。返回本次真正新增的项目。
     */
    List<Item> merge(List<Item> delivered) {
        ArrayList<Item> added = new ArrayList<>();
        if (delivered == null) {
            return added;
        }
        for (Item item : delivered) {
            if (item == null || item.id == null || item.id.isEmpty()
                    || item.text == null || item.text.isEmpty() || item.seq <= 0) {
                continue;
            }
            if (ids.contains(item.id)) {
                continue;
            }
            if (utf8Length(item.text) > MAX_TEXT_BYTES) {
                // 保存单条就会突破总量预算；不能靠淘汰其他消息腾出空间。
                continue;
            }
            add(item);
            ids.add(item.id);
            added.add(item);
        }
        if (!added.isEmpty()) {
            sort();
            evict();
        }
        return added;
    }

    /** 最新消息在前；界面与通知只读该顺序。 */
    List<Item> newestFirst() {
        ArrayList<Item> copy = new ArrayList<>(items);
        Collections.reverse(copy);
        return Collections.unmodifiableList(copy);
    }

    List<Item> oldestFirst() {
        return Collections.unmodifiableList(new ArrayList<>(items));
    }

    Item find(String id) {
        if (id == null) {
            return null;
        }
        for (Item item : items) {
            if (item.id.equals(id)) {
                return item;
            }
        }
        return null;
    }

    int unreadCount() {
        int count = 0;
        for (Item item : items) {
            if (!item.read) {
                count++;
            }
        }
        return count;
    }

    int size() {
        return items.size();
    }

    int textBytes() {
        return textBytes;
    }

    boolean markRead(String id) {
        Item current = find(id);
        if (current == null || current.read) {
            return false;
        }
        replace(current, current.asRead());
        return true;
    }

    int markAllRead() {
        int changed = 0;
        for (int i = 0; i < items.size(); i++) {
            Item item = items.get(i);
            if (!item.read) {
                items.set(i, item.asRead());
                changed++;
            }
        }
        return changed;
    }

    /** 单行摘要：换行/制表折叠为空格并按 Unicode 码点截断。 */
    static String oneLineSnippet(String text, int maxCodePoints) {
        if (text == null || maxCodePoints <= 0) {
            return "";
        }
        StringBuilder normalized = new StringBuilder(text.length());
        for (int i = 0; i < text.length(); i++) {
            char value = text.charAt(i);
            normalized.append(value == '\n' || value == '\r' || value == '\t' ? ' ' : value);
        }
        return clip(normalized.toString().trim(), maxCodePoints);
    }

    static String clip(String text, int maxCodePoints) {
        if (text == null || maxCodePoints <= 0) {
            return "";
        }
        int points = text.codePointCount(0, text.length());
        if (points <= maxCodePoints) {
            return text;
        }
        if (maxCodePoints == 1) {
            return "…";
        }
        int end = text.offsetByCodePoints(0, maxCodePoints - 1);
        return text.substring(0, end) + "…";
    }

    private void add(Item item) {
        items.add(item);
        textBytes += utf8Length(item.text);
    }

    private void replace(Item current, Item replacement) {
        int index = items.indexOf(current);
        if (index >= 0) {
            items.set(index, replacement);
        }
    }

    private void sort() {
        Collections.sort(items, (left, right) -> Long.compare(left.seq, right.seq));
    }

    private void evict() {
        while (items.size() > MAX_ITEMS || textBytes > MAX_TEXT_BYTES) {
            Item removed = items.remove(0);
            ids.remove(removed.id);
            textBytes -= utf8Length(removed.text);
        }
    }

    private static int utf8Length(String text) {
        return text.getBytes(StandardCharsets.UTF_8).length;
    }
}
