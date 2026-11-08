package com.mobileuse.vnext;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.app.NotificationManager;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.content.res.Configuration;
import android.graphics.Insets;
import android.net.Uri;
import android.os.Bundle;
import android.provider.Settings;
import android.util.Log;
import android.view.Display;
import android.view.View;
import android.view.WindowInsets;
import android.widget.Button;
import android.widget.TextView;
import android.widget.Toast;

import java.time.ZoneId;
import java.time.format.DateTimeFormatter;
import java.util.Locale;

/**
 * 用户主屏上的模式选择、状态、消息入口与停止界面。只订阅服务快照，通过显式 action 提交用户选择。
 * 主屏共享必须由本页确认对话框授权；不从启动 Intent / extras、缓存状态或 Agent 响应自动选择。
 * 所有打开路径显式指定 Display.DEFAULT_DISPLAY，但 Activity 仍校验实际 displayId；
 * 误落到虚拟屏时只结束违规任务，不重定向或授予主屏共享。
 * onStart/onStop 决定完整界面是否占用主屏，并据此临时隐藏/恢复悬浮入口；列表与详情 Dialog
 * 不会停止 Activity，因此显示期间保持隐藏。离开/重建后的后台、通知、停止及虚拟屏仍由服务管理。
 */
public final class MainActivity extends Activity {
    static final String ACTION_OPEN_MESSAGE_CENTER = "com.mobileuse.vnext.OPEN_MESSAGE_CENTER";
    static final String ACTION_OPEN_MESSAGE = "com.mobileuse.vnext.OPEN_MESSAGE";
    static final String EXTRA_MESSAGE_ID = "com.mobileuse.vnext.EXTRA_MESSAGE_ID";
    private static final String TAG = "MainActivity";
    private static final int NOTIFICATION_PERMISSION = 1;
    private final DateTimeFormatter syncTime = DateTimeFormatter
            .ofPattern("yyyy-MM-dd HH:mm:ss", Locale.CHINA).withZone(ZoneId.systemDefault());
    // 只标识本 Activity 实例；集合计数可避免配置重建时旧 onStop 清掉新 onStart。
    private final Object lifecycleIdentity = new Object();
    private final ControlApplication.Listener listener = this::render;
    private ControlApplication application;
    private MessageCenterUi messageCenter;
    private TextView backgroundStatus;
    private TextView notificationStatus;
    private TextView overlayStatus;
    private TextView stopStatus;
    private TextView stopSync;
    private TextView modeStatus;
    private TextView modeDetail;
    private TextView displayStatus;
    private TextView controlPrimary;
    private TextView controlDetail;
    private TextView serviceStatus;
    private TextView serviceDetail;
    private TextView lastSync;
    private Button start;
    private Button stop;
    private Button selectPrimary;
    private Button selectVirtual;

    @Override public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        if (!ensurePrimaryDisplay("onCreate")) {
            return;
        }
        setContentView(R.layout.activity_main);
        application = (ControlApplication) getApplication();
        messageCenter = new MessageCenterUi(this, application.messages());
        applySystemInsets();
        ((TextView) findViewById(R.id.version)).setText(
                getString(R.string.version_label, getString(R.string.app_version)));
        backgroundStatus = findViewById(R.id.background_status);
        notificationStatus = findViewById(R.id.notification_status);
        overlayStatus = findViewById(R.id.overlay_status);
        stopStatus = findViewById(R.id.stop_status);
        stopSync = findViewById(R.id.stop_sync);
        modeStatus = findViewById(R.id.mode_status);
        modeDetail = findViewById(R.id.mode_detail);
        displayStatus = findViewById(R.id.virtual_display_status);
        controlPrimary = findViewById(R.id.control_primary);
        controlDetail = findViewById(R.id.control_detail);
        serviceStatus = findViewById(R.id.service_status);
        serviceDetail = findViewById(R.id.service_detail);
        lastSync = findViewById(R.id.last_sync);
        start = findViewById(R.id.start_background);
        stop = findViewById(R.id.stop);
        selectPrimary = findViewById(R.id.select_primary);
        selectVirtual = findViewById(R.id.select_virtual);
        findViewById(R.id.open_messages).setOnClickListener(view -> messageCenter.showList());
        selectPrimary.setOnClickListener(view -> new AlertDialog.Builder(this)
                .setTitle(R.string.primary_confirmation_title)
                .setMessage(R.string.primary_confirmation_detail)
                .setNegativeButton(android.R.string.cancel, null)
                .setPositiveButton(R.string.use_primary, (dialog, which) -> selectMode(ServiceClient.MODE_PRIMARY))
                .show());
        selectVirtual.setOnClickListener(view -> selectMode(ServiceClient.MODE_VIRTUAL));
        start.setOnClickListener(view -> startBackground());
        stop.setOnClickListener(view -> startService(
                new Intent(this, ControlService.class).setAction(ControlService.ACTION_STOP)));
        findViewById(R.id.grant_notifications).setOnClickListener(view -> {
            if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, NOTIFICATION_PERMISSION);
            } else {
                openNotificationSettings();
            }
        });
        findViewById(R.id.notification_settings).setOnClickListener(view -> openNotificationSettings());
        findViewById(R.id.overlay_settings).setOnClickListener(view -> PrimaryDisplayUi.open(this,
                new Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION,
                        Uri.parse("package:" + getPackageName()))));
        // 用户打开 App 即恢复后台；通知授权仍由单独按钮请求，不阻塞前台服务启动。
        if (!application.state().backgroundRunning) {
            startBackground();
        }
        messageCenter.handleIntent(getIntent());
    }

    @Override protected void onStart() {
        super.onStart();
        if (!ensurePrimaryDisplay("onStart") || application == null) {
            return;
        }
        application.reportMainActivityVisibility(lifecycleIdentity, true);
        application.subscribe(listener);
    }

    @Override protected void onResume() {
        super.onResume();
        if (!ensurePrimaryDisplay("onResume") || application == null) {
            return;
        }
        refreshLocalState();
    }

    @Override protected void onStop() {
        if (application != null) {
            application.unsubscribe(listener);
            application.reportMainActivityVisibility(lifecycleIdentity, false);
        }
        super.onStop();
    }

    @Override protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        if (!ensurePrimaryDisplay("onNewIntent") || messageCenter == null) {
            return;
        }
        setIntent(intent);
        messageCenter.handleIntent(intent);
    }

    @Override public void onConfigurationChanged(Configuration newConfig) {
        super.onConfigurationChanged(newConfig);
        ensurePrimaryDisplay("onConfigurationChanged");
    }

    @Override public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus) {
            ensurePrimaryDisplay("onWindowFocusChanged");
        }
    }

    @Override protected void onDestroy() {
        // 正常路径由 onStop 清理；这里只幂等移除本实例，避免创建失败等销毁路径遗留可见 token。
        if (application != null) {
            application.reportMainActivityVisibility(lifecycleIdentity, false);
        }
        if (messageCenter != null) {
            messageCenter.destroy();
        }
        super.onDestroy();
    }

    /**
     * API 36 没有可覆写的 Activity 显示屏迁移回调，因此在每个可能导致界面重新可见的生命周期入口复查。
     * 违规时只结束当前任务并记录日志；不启动主屏副本，也不改变主屏共享授权。
     */
    private boolean ensurePrimaryDisplay(String lifecycleEvent) {
        if (PrimaryDisplayUi.isPrimary(this)) {
            return true;
        }
        Display display = getDisplay();
        Log.e(TAG, "Control UI left the primary display during " + lifecycleEvent
                + " (displayId=" + (display == null ? "none" : display.getDisplayId())
                + "); finishing task without reopening");
        finishAndRemoveTask();
        return false;
    }

    private void startBackground() {
        try {
            startForegroundService(new Intent(this, ControlService.class).setAction(ControlService.ACTION_START));
        } catch (IllegalStateException | SecurityException error) {
            Toast.makeText(this, "无法启动后台：" + error.getMessage(), Toast.LENGTH_LONG).show();
        }
    }

    /** 只提交显式服务 action；模式状态、代次更新及独立 STOP 都由服务主线程执行。 */
    private void selectMode(String mode) {
        try {
            startForegroundService(ControlService.selectModeIntent(this, mode));
        } catch (IllegalStateException | SecurityException error) {
            Toast.makeText(this, "无法切换屏幕模式：" + error.getMessage(), Toast.LENGTH_LONG).show();
        }
    }

    private void refreshLocalState() {
        render(application.state());
        if (application.state().backgroundRunning) {
            startService(new Intent(this, ControlService.class).setAction(ControlService.ACTION_REFRESH));
        }
    }

    @Override public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == NOTIFICATION_PERMISSION
                || requestCode == MessageCenterUi.PERMISSION_REQUEST) {
            refreshLocalState();
        }
    }

    private void openNotificationSettings() {
        NotificationManager manager = getSystemService(NotificationManager.class);
        Intent settings = new Intent(manager.getNotificationChannel(ControlService.CHANNEL_ID) == null
                ? Settings.ACTION_APP_NOTIFICATION_SETTINGS : Settings.ACTION_CHANNEL_NOTIFICATION_SETTINGS);
        settings.putExtra(Settings.EXTRA_APP_PACKAGE, getPackageName());
        settings.putExtra(Settings.EXTRA_CHANNEL_ID, ControlService.CHANNEL_ID);
        PrimaryDisplayUi.open(this, settings);
    }

    /** Android 16 强制边到边布局，主动避开状态栏、导航栏和挖孔区域。 */
    private void applySystemInsets() {
        View root = findViewById(R.id.root);
        int left = root.getPaddingLeft();
        int top = root.getPaddingTop();
        int right = root.getPaddingRight();
        int bottom = root.getPaddingBottom();
        root.setOnApplyWindowInsetsListener((view, windowInsets) -> {
            Insets insets = windowInsets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
            view.setPadding(left + insets.left, top + insets.top, right + insets.right, bottom + insets.bottom);
            return windowInsets;
        });
        root.requestApplyInsets();
    }

    private void render(ControlState state) {
        messageCenter.render(state);
        boolean primary = ServiceClient.MODE_PRIMARY.equals(state.selectedMode);
        modeStatus.setText(getString(R.string.mode_selection_value, ControlState.modeLabel(state.selectedMode))
                + (state.modeReady ? "" : " · 停止待同步"));
        modeStatus.setTextColor(getColor(!state.modeReady || primary ? R.color.status_warning : R.color.text_secondary));
        modeDetail.setText(primary ? R.string.primary_mode_detail : R.string.virtual_mode_detail);
        selectPrimary.setEnabled(state.backgroundRunning && !primary);
        selectVirtual.setEnabled(state.backgroundRunning && primary);
        renderControl(state);
        backgroundStatus.setText(state.backgroundRunning
                ? getString(R.string.background_running) : getString(R.string.background_stopped));
        backgroundStatus.setTextColor(getColor(state.backgroundRunning ? R.color.status_ok : R.color.status_warning));
        boolean permission = checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED;
        notificationStatus.setText(!permission ? "通知权限未授权：通知停止入口不可见，请授权或打开通知设置。"
                : !state.backgroundRunning ? "通知权限已授权，启动后台后检查通知频道。"
                : state.notificationsEnabled ? "通知权限已授权，后台通知频道可见。"
                : "通知权限已授权，但系统通知 / 后台通知频道已关闭。请打开通知设置。");
        overlayStatus.setText(!Settings.canDrawOverlays(this)
                ? "悬浮窗未授权：点击下方授权按钮，在列表选择“Mobile Use 控制”，允许显示在其他应用上层。通知栏仍提供停止入口。"
                : !state.backgroundRunning ? "悬浮窗已授权，启动后台后显示。"
                : state.overlayReady && !state.overlayVisible
                        ? "主屏悬浮入口已就绪；完整控制界面打开时临时隐藏，离开 App 后恢复原形态、屏幕边缘和位置。"
                : state.overlayReady ? "主屏悬浮入口已显示：支持条、窗、球、贴边把手。"
                        + "球或贴边把手先点开为条，再点“停止操作”；条 / 窗和通知栏可直接停止。"
                : state.overlayIssue != null ? state.overlayIssue : "悬浮入口正在准备，请稍后查看。");
        stopStatus.setText(state.stopReady
                ? (state.overlayVisible ? "停止可用：主屏悬浮窗、常驻通知和 App 按钮均已就绪。"
                : state.overlayReady ? "停止可用：悬浮入口随完整控制界面临时隐藏；常驻通知按钮和 App 内按钮可用。"
                : "停止可用：常驻通知按钮和 App 内按钮均已就绪。")
                : state.backgroundRunning ? "通知停止不可用；App 内仍可请求停止并同步。"
                : "停止不可用：请先启动后台。");
        VirtualDisplayHost.Descriptor display = primary ? state.primaryDisplay : state.display;
        stopSync.setText(state.stopPending
                ? (state.stopError == null ? "已阻止本地旧命令 · 停止待同步…"
                : "已阻止本地旧命令 · 停止待同步，正在按同一意图重试。"
                + String.format(Locale.CHINA, "%n") + state.stopError)
                : state.cleanupRisk() ? "控制已停止 · 资源清理未确认；旧命令不会再执行。"
                : primary ? "主屏共享中，可随时停止。" : "本地可随时停止；停止不等待服务响应。");
        stopSync.setTextColor(getColor(state.stopPending || state.cleanupRisk()
                ? R.color.status_warning : R.color.text_secondary));
        String screen = display == null ? (primary ? "主屏共享暂不可用：主屏未启用或未处于亮屏状态。"
                : "虚拟屏幕未创建 / 已释放")
                : ControlState.modeLabel(state.selectedMode)
                + String.format(Locale.CHINA, " · ID %d%n%d × %d · %d dpi · 旋转 %d°%n代次 %s",
                        display.displayId, display.width, display.height, display.densityDpi,
                        display.rotation * 90, display.generation);
        displayStatus.setText(state.displayError == null ? screen : screen + String.format("%n")
                + state.displayError);
        start.setEnabled(!state.backgroundRunning);
        stop.setEnabled(state.backgroundRunning && !state.stopPending);
        serviceStatus.setText(state.connectionTitle);
        serviceStatus.setTextColor(getColor(state.fresh() ? R.color.status_ok : R.color.status_warning));
        serviceDetail.setText(state.connectionDetail);
        lastSync.setText(state.fresh() ? getString(R.string.sync_age_seconds,
                Math.max(0, state.lastSyncElapsedMs / 1000)) : getString(R.string.never_synced));
    }

    /**
     * 最低控制视图：只显示 availability/session/work/cleanup 与本机事实。
     * 失鲜或版本不匹配时显示未知/不可用，不回显缓存中的会话或进度。
     */
    private void renderControl(ControlState state) {
        String text = state.primaryText();
        controlPrimary.setText(text);
        int color = R.color.text_secondary;
        if (state.stopPending || !state.fresh() || "blocked".equals(state.availability())) {
            color = R.color.status_error;
        } else if ("recovering".equals(state.availability()) || state.cleanupRisk()
                || !state.activeSession()) {
            color = R.color.status_warning;
        } else color = R.color.status_ok;
        controlPrimary.setTextColor(getColor(color));
        controlDetail.setText(state.overlayDetail());
    }
}
