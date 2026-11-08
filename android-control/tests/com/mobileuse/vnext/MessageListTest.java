package com.mobileuse.vnext;

import java.util.Arrays;
import java.util.List;

/** Pure JVM checks for the App message list: dedupe, read state, bounds and ordering. */
public final class MessageListTest {
    public static void main(String[] args) {
        duplicateMergeIsNoOp();
        unreadCountAndReadFlags();
        evictsOldestByCount();
        evictsOldestByByteBudget();
        rejectsSingleOversizeText();
        newestFirstOrdering();
        snippetStaysOnOneBoundedLine();
        System.out.println("MessageListTest passed");
    }

    private static void duplicateMergeIsNoOp() {
        MessageList list = new MessageList();
        list.merge(Arrays.asList(item("M1", 1, "first")));
        check(list.markRead("M1"), "first message becomes read");
        List<MessageList.Item> added = list.merge(Arrays.asList(
                new MessageList.Item("M1", 1, "changed", "2026-11-09T00:00:02Z",
                        false, "2026-11-09T00:00:03Z")));
        check(added.isEmpty(), "duplicate id returns no new item");
        check(list.size() == 1, "duplicate id does not grow the list");
        MessageList.Item stored = list.find("M1");
        check(stored != null && "first".equals(stored.text), "duplicate id keeps the original text");
        check(stored.read, "duplicate id does not clear local read state");
        check(list.unreadCount() == 0, "duplicate merge does not add unread count");
    }

    private static void unreadCountAndReadFlags() {
        MessageList list = new MessageList();
        list.merge(Arrays.asList(item("M1", 1, "a"), item("M2", 2, "b"), item("M3", 3, "c")));
        check(list.unreadCount() == 3, "three unread messages");
        check(list.markRead("M2"), "mark M2 read changes state");
        check(!list.markRead("M2"), "mark read is idempotent");
        check(list.unreadCount() == 2, "read flag lowers unread count");
        check(list.find("M2").read && !list.find("M1").read, "only M2 is read");
        check(list.markAllRead() == 2, "mark all read updates the remaining two");
        check(list.unreadCount() == 0, "no unread messages remain");
        check(list.markAllRead() == 0, "mark all read is idempotent");
    }

    private static void evictsOldestByCount() {
        MessageList list = new MessageList();
        for (int i = 1; i <= MessageList.MAX_ITEMS; i++) {
            list.merge(Arrays.asList(item("M" + i, i, "x")));
        }
        check(list.size() == MessageList.MAX_ITEMS, "count bound is reached");
        check(list.find("M1") != null, "oldest retained before overflow");
        list.merge(Arrays.asList(item("M257", 257, "x")));
        check(list.size() == MessageList.MAX_ITEMS, "count bound still holds");
        check(list.find("M1") == null, "oldest message is evicted by count");
        check(list.find("M257") != null, "newest message is retained");
        check("M2".equals(list.oldestFirst().get(0).id), "next oldest becomes first");
    }

    private static void evictsOldestByByteBudget() {
        MessageList list = new MessageList();
        String chunk = repeat('a', 300 * 1024);
        list.merge(Arrays.asList(item("M1", 1, chunk), item("M2", 2, chunk), item("M3", 3, chunk)));
        check(list.size() == 3, "three text chunks fit below the byte budget");
        check(list.textBytes() == 3 * chunk.length(), "byte accounting matches texts");
        list.merge(Arrays.asList(item("M4", 4, chunk)));
        check(list.textBytes() <= MessageList.MAX_TEXT_BYTES, "byte budget is enforced");
        check(list.find("M1") == null, "oldest message is evicted by byte budget");
        check(list.find("M2") != null && list.find("M3") != null && list.find("M4") != null,
                "newest three messages are retained");
        check(list.size() == 3, "byte eviction also keeps the item bound");
    }

    private static void rejectsSingleOversizeText() {
        MessageList list = new MessageList();
        String oversize = repeat('b', MessageList.MAX_TEXT_BYTES + 1);
        List<MessageList.Item> added = list.merge(Arrays.asList(item("M1", 1, oversize)));
        check(added.isEmpty(), "single oversize text is not stored");
        check(list.size() == 0, "oversize text does not evict into an over-budget state");
    }

    private static void newestFirstOrdering() {
        MessageList list = new MessageList();
        list.merge(Arrays.asList(item("M2", 2, "b"), item("M1", 1, "a"), item("M3", 3, "c")));
        List<MessageList.Item> newest = list.newestFirst();
        check(newest.size() == 3, "all messages listed");
        check("M3".equals(newest.get(0).id), "newest message first");
        check("M2".equals(newest.get(1).id), "middle message second");
        check("M1".equals(newest.get(2).id), "oldest message last");
        check("M1".equals(list.oldestFirst().get(0).id), "oldest-first storage order is stable");
    }

    private static void snippetStaysOnOneBoundedLine() {
        check("a b c".equals(MessageList.oneLineSnippet("a\nb\tc", 20)),
                "newlines and tabs collapse to one line");
        String clipped = MessageList.clip(repeat('x', 20), 5);
        check(clipped.codePointCount(0, clipped.length()) == 5, "clip respects code point bound");
        check(clipped.endsWith("…"), "clip marks truncation");
    }

    private static MessageList.Item item(String id, long seq, String text) {
        return new MessageList.Item(id, seq, text, "2026-11-09T00:00:00Z",
                false, "2026-11-09T00:00:01Z");
    }

    private static String repeat(char value, int count) {
        StringBuilder text = new StringBuilder(count);
        for (int i = 0; i < count; i++) {
            text.append(value);
        }
        return text.toString();
    }

    private static void check(boolean condition, String message) {
        if (!condition) {
            throw new AssertionError(message);
        }
    }
}
