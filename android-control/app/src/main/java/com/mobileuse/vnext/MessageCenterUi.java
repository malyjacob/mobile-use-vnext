package com.mobileuse.vnext;

import android.Manifest;
import android.app.AlertDialog;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.provider.Settings;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

/**
 * 控制 App 的消息入口与列表骨架。
 * P3 最小控制协议的同步与停止路径都不携带 inbox，因此这里固定渲染空收件箱：
 * 不发明第二套消息协议，也不保留旧 receipt/已读上报。通知授权入口仍由本页提供。
 */
final class MessageCenterUi {
    static final int PERMISSION_REQUEST = 2;
    private final MainActivity activity;
    private AlertDialog centerDialog;
    private LinearLayout centerRows;

    MessageCenterUi(MainActivity activity) {
        this.activity = activity;
    }

    /** 每秒只更新入口与已打开列表；空收件箱是唯一可表达的事实。 */
    void render(ControlState state) {
        Button open = activity.findViewById(R.id.open_messages);
        TextView status = activity.findViewById(R.id.message_status);
        open.setText(R.string.message_center_empty_button);
        status.setText(activity.getString(R.string.message_center_unavailable)
                + " · " + (notificationsAvailable() ? "消息通知频道可用" : "消息通知不可用"));
        if (centerDialog != null && centerDialog.isShowing()) {
            updateList();
        }
    }

    void handleIntent(Intent intent) {
        if (intent == null) {
            return;
        }
        if (MainActivity.ACTION_OPEN_MESSAGE_CENTER.equals(intent.getAction())) {
            showList();
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
            }
        });
        centerDialog = dialog;
        dialog.show();
        updateList();
    }

    void destroy() {
        if (centerDialog != null) {
            centerDialog.dismiss();
        }
    }

    private void updateList() {
        if (centerRows == null) {
            return;
        }
        centerRows.removeAllViews();
        TextView empty = new TextView(activity);
        empty.setText(R.string.message_empty);
        empty.setTextSize(15);
        empty.setTextColor(activity.getColor(R.color.text_secondary));
        empty.setPadding(0, dp(16), 0, dp(16));
        empty.setId(R.id.message_center_empty);
        centerRows.addView(empty);
    }

    private void openNotificationSettings() {
        if (activity.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED) {
            activity.requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS},
                    PERMISSION_REQUEST);
            return;
        }
        // 消息频道在 P3 不再创建；始终打开本 App 的通知总设置，避免指向不存在的频道。
        Intent settings = new Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS);
        settings.putExtra(Settings.EXTRA_APP_PACKAGE, activity.getPackageName());
        PrimaryDisplayUi.open(activity, settings);
    }

    private boolean notificationsAvailable() {
        return ControlService.notificationsEnabled(activity, ControlService.CHANNEL_ID);
    }

    private int dp(int value) {
        return Math.round(value * activity.getResources().getDisplayMetrics().density);
    }
}
