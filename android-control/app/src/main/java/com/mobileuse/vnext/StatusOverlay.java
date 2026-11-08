package com.mobileuse.vnext;

import android.content.Context;
import android.content.SharedPreferences;
import android.graphics.Insets;
import android.graphics.PixelFormat;
import android.graphics.Rect;
import android.graphics.drawable.GradientDrawable;
import android.hardware.display.DisplayManager;
import android.provider.Settings;
import android.view.Display;
import android.view.Gravity;
import android.view.LayoutInflater;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.WindowInsets;
import android.view.WindowManager;
import android.view.WindowMetrics;
import android.widget.Button;
import android.widget.TextView;
import java.util.Collections;
import java.util.Locale;

/**
 * 主线程拥有的四态主屏入口；只渲染服务快照，所有停止仍调用同一个服务回调。
 * 偏好仅含 UI 形态/边缘/归一化位置，绝不保存会话或主屏共享授权。
 * MainActivity 的完整界面处于 started 时仅临时隐藏窗口，不改变这些 UI 偏好。
 */
final class StatusOverlay implements AutoCloseable {
    private enum Form { ball, edge_handle, bar, window }
    private final Context context;
    private final WindowManager windows;
    private final SharedPreferences preferences;
    private final Runnable stop;
    private View root;
    private WindowManager.LayoutParams placement;
    private Form form;
    private boolean right;
    private float positionX;
    private float positionY;
    private ControlState state;
    private boolean temporarilyHidden;
    private String issue;
    private float downX, downY;
    private int initialX, initialY;
    private boolean dragging;

    StatusOverlay(Context service, Runnable stop) {
        Display display = service.getSystemService(DisplayManager.class).getDisplay(Display.DEFAULT_DISPLAY);
        context = service.createDisplayContext(display)
                .createWindowContext(WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY, null);
        windows = context.getSystemService(WindowManager.class);
        preferences = service.getSharedPreferences("overlay_ui", Context.MODE_PRIVATE);
        form = Form.valueOf(preferences.getString("form", "ball"));
        right = preferences.getBoolean("right", true);
        positionX = preferences.getFloat("x", 1f);
        positionY = preferences.getFloat("y", .15f);
        this.stop = stop;
    }

    /** 每拍复查授权，恢复用户最近形态；新建窗口在添加前即应用临时隐藏，避免前台闪现。 */
    void refreshPermission() {
        if (!Settings.canDrawOverlays(context)) {
            close();
            issue = "悬浮窗未授权；请允许显示在其他应用上层。通知栏仍可停止。";
        } else if (root == null) {
            attach();
        }
    }

    private boolean compact() {
        return form == Form.ball || form == Form.edge_handle;
    }

    private void attach() {
        int layout = form == Form.ball ? R.layout.overlay_ball
                : form == Form.edge_handle ? R.layout.overlay_edge_handle : R.layout.overlay_status;
        View view = LayoutInflater.from(context).inflate(layout, null);
        // 真正缩小 WM 窗口及触摸矩形；NOT_FOCUSABLE 保留用户应用/输入法焦点。
        placement = new WindowManager.LayoutParams(width(), compact() ? dp(48)
                : WindowManager.LayoutParams.WRAP_CONTENT, WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
                WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE, PixelFormat.TRANSLUCENT);
        placement.gravity = Gravity.TOP | Gravity.LEFT;
        placement.setTitle("MobileUseStatusOverlay:" + form.name());
        placement.setFitInsetsTypes(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
        if (compact()) {
            // 只申请小窗口自身的手势区域；小米 GestureStub 仍保留最外侧返回区。
            view.setSystemGestureExclusionRects(Collections.singletonList(new Rect(0, 0, width(), dp(48))));
            view.setOnClickListener(handle -> change(Form.bar));
            view.setOnTouchListener(this::drag);
            if (form == Form.edge_handle) {
                GradientDrawable background = new GradientDrawable();
                background.setColor(0xFF334155);
                float radius = dp(24);
                background.setCornerRadii(right ? new float[]{radius,radius,0,0,0,0,radius,radius}
                        : new float[]{0,0,radius,radius,radius,radius,0,0});
                view.setBackground(background);
                TextView label = view.findViewById(R.id.overlay_compact_text);
                label.setMaxLines(2);
                label.setTextSize(10);
                // 本机返回触摸条宽约 22dp；40dp 把手将箭头/抓取区留在内侧，
                // 不扩大透明触摸区，也不禁用用户整条屏边的返回手势。
                label.setPadding(right ? 0 : dp(18), 0, right ? dp(18) : 0, 0);
            }
        } else {
            View title = view.findViewById(R.id.overlay_title);
            title.setOnClickListener(handle -> change(form == Form.bar ? Form.window : Form.bar));
            title.setOnTouchListener(this::drag);
            view.findViewById(R.id.overlay_details).setVisibility(form == Form.window ? View.VISIBLE : View.GONE);
            ((TextView) view.findViewById(R.id.overlay_progress)).setLines(1);
            ((TextView) view.findViewById(R.id.overlay_detail_text)).setLines(8);
            view.findViewById(R.id.overlay_stop).setOnClickListener(button -> stop.run());
            view.findViewById(R.id.overlay_ball).setOnClickListener(button -> change(Form.ball));
            view.findViewById(R.id.overlay_edge).setOnClickListener(button -> {
                right = placement.x + placement.width / 2 >= availableBounds().width() / 2;
                change(Form.edge_handle);
            });
            view.findViewById(R.id.overlay_messages).setOnClickListener(button -> PrimaryDisplayUi.open(context,
                    PrimaryDisplayUi.controlIntent(context).setAction(MainActivity.ACTION_OPEN_MESSAGE_CENTER)));
            view.findViewById(R.id.overlay_open).setOnClickListener(button -> PrimaryDisplayUi.open(context,
                    PrimaryDisplayUi.controlIntent(context)));
        }
        try {
            view.setVisibility(temporarilyHidden ? View.GONE : View.VISIBLE);
            windows.addView(view, placement);
            root = view;
            issue = null;
            if (state != null) render(state);
            // GONE 时高度为 0，不能在此计算位置；恢复显示后由 setTemporarilyHidden 重新布局。
            view.post(() -> {
                if (root == view && !temporarilyHidden) {
                    reposition();
                }
            });
        } catch (SecurityException | WindowManager.BadTokenException error) {
            issue = "系统未允许显示悬浮窗；请检查悬浮权限及小米后台弹出界面设置。通知栏仍可停止。";
        } catch (RuntimeException error) {
            if (!isInputChannelInitializationFailure(error)) {
                throw error;
            }
            // WindowManagerGlobal 已在 setView 失败时回滚本次 addView；这是本机观察到的
            // 瞬态 InputChannel 初始化失败。保留服务、通知 STOP 与心跳，下一拍刷新会自然重建窗口。
            issue = "系统窗口输入通道暂未就绪；通知栏仍可停止，将继续刷新悬浮窗。";
        }
    }

    /** 只放过本机已观察到的瞬态输入通道初始化失败，其他运行时异常仍由服务暴露并处理。 */
    private static boolean isInputChannelInitializationFailure(RuntimeException error) {
        for (Throwable cause = error; cause != null; cause = cause.getCause()) {
            if (cause instanceof RuntimeException
                    && "InputChannel is not initialized.".equals(cause.getMessage())) {
                return true;
            }
        }
        return false;
    }

    private int width() {
        return Math.min(dp(form == Form.ball ? 48 : form == Form.edge_handle ? 40 : 288),
                availableBounds().width());
    }

    private void change(Form next) {
        rememberPosition();
        close();
        form = next;
        attach();
        save();
    }

    boolean ready() { return root != null && root.isAttachedToWindow(); }
    boolean visible() { return ready() && !temporarilyHidden && root.getVisibility() == View.VISIBLE; }
    String issue() { return issue; }

    /**
     * 只临时切换窗口可见性，供 MainActivity 的 onStart/onStop 使用。
     * 不修改 form/right/positionX/positionY，也不写入 SharedPreferences；恢复后按原位置重新布局。
     */
    void setTemporarilyHidden(boolean hidden) {
        if (temporarilyHidden == hidden) {
            return;
        }
        temporarilyHidden = hidden;
        View view = root;
        if (view == null) {
            return;
        }
        view.setVisibility(hidden ? View.GONE : View.VISIBLE);
        if (!hidden) {
            // GONE 期间高度为 0；等 VISIBLE 完成布局后再按保存的比例恢复实际位置。
            view.post(() -> {
                if (root == view && !temporarilyHidden && view.getVisibility() == View.VISIBLE) {
                    reposition();
                }
            });
        }
    }

    /**
     * 仅更新内容，不展开、不写偏好、不移动、不请求焦点。
     * 消息中心骨架当前无服务端邮局，未读徽标固定不显示；停止入口始终保留。
     */
    void render(ControlState snapshot) {
        state = snapshot;
        if (root == null) return;
        if (compact()) {
            String status = compactStatus();
            String mode = ServiceClient.MODE_PRIMARY.equals(state.selectedMode) ? "主" : "虚";
            String compactLine = String.format(Locale.CHINA, "%n");
            ((TextView) root.findViewById(R.id.overlay_compact_text)).setText(form == Form.edge_handle
                    ? (right ? "‹ " : "› ") + mode + compactLine + status
                    : mode + compactLine + status);
            TextView badge = root.findViewById(R.id.overlay_compact_unread);
            badge.setVisibility(View.GONE);
            root.setContentDescription(state.primaryText() + "；点击展开为条后可停止；拖动可移动");
            return;
        }
        ((TextView) root.findViewById(R.id.overlay_title)).setText(state.overlayTitle());
        ((TextView) root.findViewById(R.id.overlay_progress)).setText(state.overlayProgress());
        ((TextView) root.findViewById(R.id.overlay_detail_text)).setText(state.overlayDetail());
        TextView badge = root.findViewById(R.id.overlay_unread);
        badge.setVisibility(View.GONE);
        ((Button) root.findViewById(R.id.overlay_messages)).setText(
                context.getString(R.string.message_center_empty_button));
        Button button = root.findViewById(R.id.overlay_stop);
        button.setText(state.stopPending ? "停止中" : "停止操作");
        // 停止随时可用：它不依赖连接、契约版本或服务可用性。
        button.setEnabled(state.backgroundRunning && !state.stopPending);
    }

    /** 把手内侧只有两字宽；用完整短词避免“运行中”被省略成“运…”。 */
    private String compactStatus() {
        if (state.stopPending) return "停中";
        if (!state.fresh()) return "断线";
        if ("recovering".equals(state.availability())) return "核验";
        if ("blocked".equals(state.availability())) return "阻塞";
        if (state.cleanupRisk()) return "清理";
        ServiceClient.Session session = state.session();
        if (session == null) return "待命";
        if ("starting".equals(session.state)) return "准备";
        if ("ended".equals(session.state)) return "结束";
        ServiceClient.Work work = state.work();
        return work == null ? "空闲" : "运行";
    }

    /** 只有球/把手或大形态标题注册拖动；超过系统 slop 才移动，拖动结束不合成点击。 */
    private boolean drag(View handle, MotionEvent event) {
        switch (event.getActionMasked()) {
            case MotionEvent.ACTION_DOWN:
                downX = event.getRawX(); downY = event.getRawY();
                initialX = placement.x; initialY = placement.y;
                dragging = false;
                return true;
            case MotionEvent.ACTION_MOVE:
                float dx = event.getRawX() - downX, dy = event.getRawY() - downY;
                int slop = ViewConfiguration.get(context).getScaledTouchSlop();
                dragging |= Math.abs(dx) > slop || Math.abs(dy) > slop;
                if (dragging) {
                    placement.x = initialX + Math.round(dx);
                    placement.y = initialY + Math.round(dy);
                    clamp();
                }
                return true;
            case MotionEvent.ACTION_UP:
                if (!dragging) {
                    handle.performClick();
                } else {
                    // 沿边只改变高度；向屏内拖出一个球的距离后，在手势结束恢复球。
                    boolean pulled = form == Form.edge_handle && (right
                            ? initialX - placement.x > dp(48) : placement.x - initialX > dp(48));
                    boolean atEdge = placement.x <= dp(16)
                            || placement.x + placement.width >= availableBounds().width() - dp(16);
                    if (form == Form.ball && atEdge) {
                        right = placement.x > availableBounds().width() / 2;
                        change(Form.edge_handle);
                    } else if (pulled) {
                        change(Form.ball);
                    } else {
                        if (form == Form.edge_handle) placement.x = right ? availableBounds().width() - placement.width : 0;
                        clamp(); rememberPosition(); save();
                    }
                }
                return true;
            case MotionEvent.ACTION_CANCEL:
                // 系统取消拖动时不触发点击；保存最终合法位置。
                if (form == Form.edge_handle) placement.x = right ? availableBounds().width() - placement.width : 0;
                clamp(); rememberPosition(); save();
                return true;
            default: return false;
        }
    }

    /** 只在用户转换/拖动或主屏配置变化时调用，按新的系统栏/挖孔可用边界恢复比例。 */
    void reposition() {
        if (root == null || temporarilyHidden) return;
        Rect bounds = availableBounds();
        placement.width = width();
        placement.x = form == Form.edge_handle ? (right ? bounds.width() - placement.width : 0)
                : Math.round(positionX * Math.max(0, bounds.width() - placement.width));
        placement.y = Math.round(positionY * Math.max(0, bounds.height() - root.getHeight()));
        clamp();
    }

    private void clamp() {
        Rect bounds = availableBounds();
        placement.x = Math.max(0, Math.min(placement.x, bounds.width() - placement.width));
        placement.y = Math.max(0, Math.min(placement.y, bounds.height() - root.getHeight()));
        windows.updateViewLayout(root, placement);
    }

    private void rememberPosition() {
        if (root == null) return;
        Rect bounds = availableBounds();
        positionX = (float) placement.x / Math.max(1, bounds.width() - placement.width);
        positionY = (float) placement.y / Math.max(1, bounds.height() - root.getHeight());
    }

    private void save() {
        preferences.edit().putString("form", form.name()).putBoolean("right", right)
                .putFloat("x", positionX).putFloat("y", positionY).apply();
    }

    private Rect availableBounds() {
        WindowMetrics metrics = windows.getCurrentWindowMetrics();
        Insets insets = metrics.getWindowInsets().getInsetsIgnoringVisibility(
                WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
        Rect bounds = new Rect(metrics.getBounds());
        bounds.inset(insets.left, insets.top, insets.right, insets.bottom);
        return bounds;
    }
    private int dp(int value) { return Math.round(value * context.getResources().getDisplayMetrics().density); }

    @Override public void close() {
        if (root != null) { windows.removeView(root); root = null; }
    }
}
