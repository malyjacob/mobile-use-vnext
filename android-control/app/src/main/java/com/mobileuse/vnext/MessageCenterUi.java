package com.mobileuse.vnext;

import android.Manifest;
import android.app.AlertDialog;
import android.app.NotificationManager;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Typeface;
import android.provider.Settings;
import android.text.TextUtils;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import java.util.List;

/**
 * Agent 消息入口、列表与详情。正文只来自本机 MessageStore；已读状态仅在本机保存，
 * 不进入同步请求、服务日志或服务端状态。通知授权入口仍由本页提供。
 */
final class MessageCenterUi {
    static final int PERMISSION_REQUEST = 2;
    private static final int SNIPPET_CODE_POINTS = 80;
    private final MainActivity activity;
    private final MessageStore messages;
    private AlertDialog centerDialog;
    private AlertDialog detailDialog;
    private LinearLayout centerRows;
    private Button allRead;
    private long renderedRevision = -1;

    MessageCenterUi(MainActivity activity, MessageStore messages) {
        this.activity = activity;
        this.messages = messages;
    }

    /** 每秒只刷新入口与状态行；列表只在本地消息版本变化时重建，避免打断滚动和点击。 */
    void render(ControlState state) {
        updateEntry();
        if (centerDialog != null && centerDialog.isShowing()
                && renderedRevision != messages.revision()) {
            updateList();
        }
    }

    void handleIntent(Intent intent) {
        if (intent == null) {
            return;
        }
        String action = intent.getAction();
        if (MainActivity.ACTION_OPEN_MESSAGE_CENTER.equals(action)) {
            showList();
        } else if (MainActivity.ACTION_OPEN_MESSAGE.equals(action)) {
            String id = intent.getStringExtra(MainActivity.EXTRA_MESSAGE_ID);
            if (id == null || messages.find(id) == null) {
                showList();
                return;
            }
            showList();
            showDetail(id);
        }
    }

    void showList() {
        if (centerDialog != null && centerDialog.isShowing()) {
            updateList();
            return;
        }
        ScrollView scroll = new ScrollView(activity);
        LinearLayout content = new LinearLayout(activity);
        content.setOrientation(LinearLayout.VERTICAL);
        content.setPadding(dp(24), 0, dp(24), dp(8));
        scroll.addView(content, new ScrollView.LayoutParams(
                ScrollView.LayoutParams.MATCH_PARENT, ScrollView.LayoutParams.WRAP_CONTENT));

        TextView explanation = new TextView(activity);
        explanation.setId(R.id.message_center_authorization);
        explanation.setTextSize(14);
        explanation.setTextColor(activity.getColor(R.color.text_secondary));
        explanation.setPadding(0, 0, 0, dp(8));
        explanation.setText(R.string.message_authorization_detail);
        content.addView(explanation);

        Button settings = new Button(activity);
        settings.setId(R.id.message_center_settings);
        settings.setText(R.string.message_settings_button);
        settings.setMinHeight(dp(48));
        settings.setOnClickListener(view -> openNotificationSettings());
        content.addView(settings);

        allRead = new Button(activity);
        allRead.setText(R.string.message_center_all_read);
        allRead.setMinHeight(dp(48));
        allRead.setOnClickListener(view -> {
            messages.markAllRead();
            updateEntry();
            updateList();
        });
        content.addView(allRead);

        centerRows = new LinearLayout(activity);
        centerRows.setId(R.id.message_center_rows);
        centerRows.setOrientation(LinearLayout.VERTICAL);
        content.addView(centerRows);

        AlertDialog dialog = new AlertDialog.Builder(activity)
                .setTitle(R.string.message_center_title)
                .setView(scroll)
                .setNegativeButton(R.string.message_center_close, null)
                .create();
        dialog.setOnDismissListener(ignored -> {
            if (centerDialog == dialog) {
                centerDialog = null;
                centerRows = null;
                allRead = null;
                renderedRevision = -1;
            }
        });
        centerDialog = dialog;
        dialog.show();
        updateList();
    }

    void destroy() {
        if (detailDialog != null) {
            detailDialog.dismiss();
            detailDialog = null;
        }
        if (centerDialog != null) {
            centerDialog.dismiss();
            centerDialog = null;
        }
        centerRows = null;
        allRead = null;
    }

    private void updateEntry() {
        Button open = activity.findViewById(R.id.open_messages);
        if (open != null) {
            open.setText(activity.getString(R.string.message_center_button, messages.unreadCount()));
        }
        TextView status = activity.findViewById(R.id.message_status);
        if (status != null) {
            status.setText(activity.getString(R.string.message_status_line,
                    messages.unreadCount(), messages.totalCount(),
                    notificationsAvailable() ? activity.getString(R.string.message_channel_available)
                            : activity.getString(R.string.message_channel_unavailable)));
        }
    }

    private void updateList() {
        if (centerRows == null) {
            return;
        }
        renderedRevision = messages.revision();
        centerRows.removeAllViews();
        if (allRead != null) {
            allRead.setEnabled(messages.unreadCount() > 0);
        }
        List<MessageList.Item> items = messages.listNewestFirst();
        if (items.isEmpty()) {
            TextView empty = new TextView(activity);
            empty.setId(R.id.message_center_empty);
            empty.setText(R.string.message_empty);
            empty.setTextSize(15);
            empty.setTextColor(activity.getColor(R.color.text_secondary));
            empty.setPadding(0, dp(16), 0, dp(16));
            centerRows.addView(empty);
            return;
        }
        for (MessageList.Item item : items) {
            centerRows.addView(row(item));
        }
    }

    private View row(MessageList.Item item) {
        LinearLayout row = new LinearLayout(activity);
        row.setOrientation(LinearLayout.VERTICAL);
        row.setPadding(dp(8), dp(10), dp(8), dp(10));
        row.setBackgroundResource(android.R.drawable.list_selector_background);
        row.setClickable(true);
        row.setFocusable(true);

        TextView meta = new TextView(activity);
        meta.setTextSize(13);
        meta.setTextColor(activity.getColor(R.color.text_secondary));
        meta.setText(activity.getString(item.read ? R.string.message_row_meta_read
                        : R.string.message_row_meta_unread,
                ControlState.timeLabel(item.createdAt)));
        row.addView(meta);

        TextView snippet = new TextView(activity);
        snippet.setTextSize(15);
        snippet.setTextColor(activity.getColor(item.read ? R.color.text_secondary : R.color.text_primary));
        snippet.setMaxLines(1);
        snippet.setEllipsize(TextUtils.TruncateAt.END);
        snippet.setText(MessageList.oneLineSnippet(item.text, SNIPPET_CODE_POINTS));
        if (!item.read) {
            snippet.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        }
        row.addView(snippet);
        row.setContentDescription(activity.getString(item.read
                ? R.string.message_row_description_read : R.string.message_row_description_unread,
                ControlState.timeLabel(item.createdAt)));
        row.setOnClickListener(view -> showDetail(item.id));
        return row;
    }

    private void showDetail(String id) {
        MessageList.Item item = messages.find(id);
        if (item == null) {
            showList();
            return;
        }
        messages.markRead(id);
        updateEntry();
        if (centerDialog != null && centerDialog.isShowing()) {
            updateList();
        }
        if (detailDialog != null && detailDialog.isShowing()) {
            detailDialog.dismiss();
        }
        String body = item.text
                + "\n\n" + activity.getString(R.string.message_detail_created,
                        ControlState.timeLabel(item.createdAt))
                + "\n" + activity.getString(R.string.message_detail_received,
                        ControlState.timeLabel(item.receivedAt));
        AlertDialog dialog = new AlertDialog.Builder(activity)
                .setTitle(R.string.message_detail_title)
                .setMessage(body)
                .setPositiveButton(R.string.message_center_close, null)
                .create();
        dialog.setOnDismissListener(ignored -> {
            if (detailDialog == dialog) {
                detailDialog = null;
            }
        });
        detailDialog = dialog;
        dialog.show();
    }

    private void openNotificationSettings() {
        if (activity.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED) {
            activity.requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS},
                    PERMISSION_REQUEST);
            return;
        }
        NotificationManager manager = activity.getSystemService(NotificationManager.class);
        String action = manager.getNotificationChannel(ControlService.CHANNEL_ID_MESSAGES) == null
                ? Settings.ACTION_APP_NOTIFICATION_SETTINGS
                : Settings.ACTION_CHANNEL_NOTIFICATION_SETTINGS;
        Intent settings = new Intent(action);
        settings.putExtra(Settings.EXTRA_APP_PACKAGE, activity.getPackageName());
        settings.putExtra(Settings.EXTRA_CHANNEL_ID, ControlService.CHANNEL_ID_MESSAGES);
        PrimaryDisplayUi.open(activity, settings);
    }

    private boolean notificationsAvailable() {
        return ControlService.notificationsEnabled(activity, ControlService.CHANNEL_ID_MESSAGES);
    }

    private int dp(int value) {
        return Math.round(value * activity.getResources().getDisplayMetrics().density);
    }
}
