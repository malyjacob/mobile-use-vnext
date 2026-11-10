package com.mobileuse.vnext;

import java.time.Instant;
import java.util.Locale;

/**
 * 服务在主线程发布的 UI 快照：只含服务投影的 control 与本机事实（模式选择、停止待同步、
 * 连接新鲜度、屏幕与悬浮状态）。主文案映射见 docs/control-protocol.md §5。
 * 不保存第二份会话权威：失鲜后 control 不再参与主文案，也不保留缓存中的“正在运行”。
 */
final class ControlState {
    /** 连续 6 秒没有成功响应即判失鲜；界面进入未知，不沿用缓存 active。 */
    static final long STALE_AFTER_MS = 6000;
    final boolean backgroundRunning;
    final boolean notificationsEnabled;
    // ready 表示悬浮窗口仍已附着且权限可用；visible 另行表示当前是否实际显示。
    final boolean overlayReady;
    final boolean overlayVisible;
    final String overlayIssue;
    final boolean stopReady;
    final boolean stopPending;
    final long stopSeq;
    final String stopError;
    final String selectedMode;
    // 与同步请求相同：本地停止未确认时模式不就绪。
    final boolean modeReady;
    final VirtualDisplayHost.Descriptor primaryDisplay;
    final VirtualDisplayHost.Descriptor display;
    final String displayError;
    /** App 仍持有退休尝试的收尾事实；只用于详情，不覆盖当前会话或模式文案。 */
    final boolean cleanupUnconfirmed;
    final boolean connected;
    final long lastSyncElapsedMs;
    final String connectionTitle;
    final String connectionDetail;
    /** 最近一次成功同步的控制视图；serviceInstanceId 为空表示本 App 实例从未同步成功。 */
    final String serviceInstanceId;
    final ServiceClient.ControlView control;
    /** App 私有消息存储的未读数，供悬浮入口与通知入口共用同一快照。 */
    final int unreadMessages;
    /** 只读展示用：最近一次观察到的已结束会话摘要，不参与主文案、授权或准入。 */
    final String lastEndedSessionSummary;
    /** 只读展示用：最近一次观察到的设备工作摘要，不参与主文案、授权或准入。 */
    final String lastObservedWorkSummary;

    ControlState(boolean backgroundRunning, boolean notificationsEnabled, boolean overlayReady,
            boolean overlayVisible, String overlayIssue, boolean stopReady,
            boolean stopPending, long stopSeq, String stopError,
            String selectedMode, VirtualDisplayHost.Descriptor primaryDisplay,
            VirtualDisplayHost.Descriptor display, String displayError, boolean cleanupUnconfirmed,
            boolean connected, long lastSyncElapsedMs,
            String connectionTitle, String connectionDetail, String serviceInstanceId,
            ServiceClient.ControlView control, int unreadMessages,
            String lastEndedSessionSummary, String lastObservedWorkSummary) {
        this.backgroundRunning = backgroundRunning;
        this.notificationsEnabled = notificationsEnabled;
        this.overlayReady = overlayReady;
        this.overlayVisible = overlayVisible;
        this.overlayIssue = overlayIssue;
        this.stopReady = stopReady;
        this.stopPending = stopPending;
        this.stopSeq = stopSeq;
        this.stopError = stopError;
        this.selectedMode = selectedMode;
        this.modeReady = !stopPending;
        this.primaryDisplay = primaryDisplay;
        this.display = display;
        this.displayError = displayError;
        this.cleanupUnconfirmed = cleanupUnconfirmed;
        this.connected = connected;
        this.lastSyncElapsedMs = lastSyncElapsedMs;
        this.connectionTitle = connectionTitle;
        this.connectionDetail = connectionDetail;
        this.serviceInstanceId = serviceInstanceId;
        this.control = control;
        this.unreadMessages = unreadMessages;
        this.lastEndedSessionSummary = lastEndedSessionSummary;
        this.lastObservedWorkSummary = lastObservedWorkSummary;
    }

    static ControlState inactive() {
        return new ControlState(false, false, false, false, null, false, false, 0, null,
                ServiceClient.MODE_VIRTUAL, null, null, null, false,
                false, -1, "后台未启动", "点击“启动后台”后持续同步；离开页面不影响服务。", null, null,
                0, null, null);
    }

    /** 连接失鲜或协议失败后不再使用缓存 control；只有新鲜成功响应才可回答服务事实。 */
    boolean fresh() {
        return connected && control != null && lastSyncElapsedMs >= 0
                && lastSyncElapsedMs <= STALE_AFTER_MS;
    }

    ServiceClient.Session session() {
        return fresh() ? control.session : null;
    }

    ServiceClient.Work work() {
        return fresh() ? control.work : null;
    }

    ServiceClient.Cleanup cleanup() {
        return fresh() ? control.cleanup : null;
    }

    String availability() {
        return fresh() ? control.availability.state : null;
    }

    String availabilityReason() {
        return fresh() ? control.availability.reason : null;
    }

    /**
     * 主文案优先级：本地停止待同步 > 连接失鲜 > 当前可用性 > 当前会话/工作 > 待命。
     * retired attempt 的清理事实只进入详情，不覆盖新 current。
     */
    String primaryText() {
        if (stopPending) {
            return "已阻止本地旧命令 · 停止待同步";
        }
        if (!fresh()) {
            return "连接中断 · 服务状态未知";
        }
        String availability = control.availability.state;
        if ("recovering".equals(availability)) {
            return "正在核验当前控制能力";
        }
        if ("blocked".equals(availability)) {
            return "控制不可用 · 查看原因";
        }
        ServiceClient.Session session = control.session;
        if (session == null) {
            return modeLabel(selectedMode) + " · 待命";
        }
        String agent = hasText(session.agent) ? session.agent.trim() : "Agent";
        if ("starting".equals(session.state)) {
            return agent + " · 正在准备";
        }
        if ("ended".equals(session.state)) {
            return "上次会话已结束" + reasonSuffix(session.reason);
        }
        ServiceClient.Work work = control.work;
        if (work == null) {
            return agent + " · 会话有效，当前无设备操作";
        }
        return agent + " · " + workText(work.kind);
    }

    /** 常驻通知与悬浮入口共用的短文案；与主文案同一事实来源。 */
    String summary() {
        return primaryText();
    }

    boolean activeSession() {
        ServiceClient.Session session = session();
        return session != null && "active".equals(session.state);
    }

    /** 悬浮标题：优先显示停止/断线，其次服务控制视图。 */
    String overlayTitle() {
        if (stopPending) {
            return "停止待同步";
        }
        if (!fresh()) {
            return "连接中断 · 状态未知";
        }
        return primaryText();
    }

    /** 悬浮进度行只表达 work 类别，不显示节点、坐标、输入正文或命令。 */
    String overlayProgress() {
        if (stopPending || !fresh() || control.session == null) {
            return primaryText();
        }
        ServiceClient.Work work = control.work;
        if (work == null) {
            return "阶段：空闲";
        }
        return "阶段：" + workText(work.kind) + phaseSuffix(work.phase);
    }

    /**
     * 展开详情：同一不可变快照中的停止同步、连接新鲜度、可用性、会话、工作、五项清理、
     * 所选模式与通知/悬浮风险。历史摘要只在快照新鲜且当前无会话/工作时追加。
     */
    String overlayDetail() {
        StringBuilder detail = new StringBuilder(primaryText());
        detail.append("\n停止同步：").append(stopPending ? "待同步" : "已同步");
        if (stopPending) {
            detail.append(" · 序号 ").append(stopSeq)
                    .append(stopError == null ? " · 正在同步服务" : " · 上次同步失败，重试中");
        }
        if (!fresh()) {
            detail.append("\n连接：已中断 · ").append(connectionTitle);
            if (hasText(connectionDetail)) {
                detail.append(" · ").append(connectionDetail);
            }
            detail.append("\n可用性：未知");
            detail.append("\n会话：未知（不沿用缓存）");
            detail.append("\n设备操作：未知");
            detail.append("\n资源清理：未知");
            appendLocalFacts(detail);
            return detail.toString();
        }
        detail.append("\n连接：已连接 · ")
                .append(Math.max(0, lastSyncElapsedMs / 1000))
                .append(" 秒前成功同步（").append(STALE_AFTER_MS / 1000).append(" 秒失鲜）");
        detail.append("\n可用性：").append(availabilityLabel(control.availability.state));
        if (hasText(control.availability.reason)) {
            detail.append(" · 原因：").append(reasonLabel(control.availability.reason.trim()));
        }
        appendSession(detail, control.session);
        appendWork(detail, control.work);
        detail.append(cleanupText(control.cleanup));
        appendLocalFacts(detail);
        if (control.session == null && control.work == null) {
            if (hasText(lastEndedSessionSummary)) {
                detail.append("\n上次会话：").append(lastEndedSessionSummary);
            }
            if (hasText(lastObservedWorkSummary)) {
                detail.append("\n上次操作：").append(lastObservedWorkSummary);
            }
        }
        return detail.toString();
    }

    private static void appendSession(StringBuilder detail, ServiceClient.Session session) {
        if (session == null) {
            detail.append("\n会话：无");
            return;
        }
        detail.append("\n会话：").append(modeLabel(session.mode));
        if (hasText(session.origin)) {
            detail.append(" · 来源 ").append(session.origin.trim());
        }
        if (hasText(session.agent)) {
            detail.append(" · ").append(session.agent.trim());
        }
        detail.append(" · ").append(sessionStateLabel(session.state));
        if (hasText(session.startedAt)) {
            detail.append("\n开始：").append(timeLabel(session.startedAt));
        }
        if ("ended".equals(session.state)) {
            if (hasText(session.endedAt)) {
                detail.append("\n结束：").append(timeLabel(session.endedAt));
            }
            detail.append(reasonLine("结束原因", session.reason));
        } else {
            if (hasText(session.expiresAt)) {
                detail.append("\n有效至：").append(timeLabel(session.expiresAt));
            }
            detail.append(reasonLine("原因", session.reason));
        }
    }

    private static void appendWork(StringBuilder detail, ServiceClient.Work work) {
        if (work == null) {
            detail.append("\n设备操作：无");
            return;
        }
        detail.append("\n设备操作：").append(workText(work.kind));
        detail.append("\n阶段：").append(phaseLabel(work.phase));
    }

    private void appendLocalFacts(StringBuilder detail) {
        detail.append("\n所选模式：").append(modeLabel(selectedMode));
        if (cleanupUnconfirmed) {
            detail.append("\n本机退休显示：仍有尝试等待真实终结或物理收尾");
        }
        detail.append("\n通知：").append(notificationsEnabled
                ? "可用" : "不可用 · 停止入口可能不可见");
        if (overlayIssue != null) {
            detail.append("\n悬浮窗：不可用 · ").append(overlayIssue);
        } else if (overlayReady) {
            detail.append("\n悬浮窗：可用")
                    .append(overlayVisible ? "（已显示）" : "（完整控制界面打开时隐藏）");
        } else {
            detail.append("\n悬浮窗：准备中或不可用");
        }
    }

    /**
     * Retired cleanup never rewrites a fresh current projection. The compatibility method
     * only lets local history mark an otherwise stale/unknown view.
     */
    boolean cleanupRisk() {
        return cleanupUnconfirmed && !fresh();
    }

    private static String cleanupText(ServiceClient.Cleanup cleanup) {
        if (cleanup == null) {
            return "\n资源清理：不适用";
        }
        StringBuilder text = new StringBuilder("\n资源清理：").append(cleanupLabel(cleanup.state));
        appendPart(text, "执行链", cleanup.execution);
        appendPart(text, "任务", cleanup.tasks);
        appendPart(text, "最近任务", cleanup.recents);
        appendPart(text, "显示", cleanup.display);
        return text.toString();
    }

    private static void appendPart(StringBuilder text, String label, String value) {
        text.append(" · ").append(label).append(' ')
                .append(hasText(value) ? cleanupLabel(value.trim()) : "不适用");
    }

    /** 选择值和会话 mode 都已在协议/Intent 边界校验；各入口统一使用可辨认的模式名称。 */
    static String modeLabel(String mode) {
        return ServiceClient.MODE_PRIMARY.equals(mode) ? "主屏共享" : "虚拟屏";
    }

    static String availabilityLabel(String state) {
        if ("ready".equals(state)) {
            return "就绪";
        }
        if ("recovering".equals(state)) {
            return "核验中";
        }
        if ("blocked".equals(state)) {
            return "不可用";
        }
        return "未知";
    }

    static String sessionStateLabel(String state) {
        if ("starting".equals(state)) {
            return "正在准备";
        }
        if ("active".equals(state)) {
            return "会话有效";
        }
        if ("ended".equals(state)) {
            return "已结束";
        }
        return "未知";
    }

    /** work.kind 只有类别；未知类别不展开任何执行材料，统一显示通用文案。 */
    static String workText(String kind) {
        if ("act".equals(kind)) {
            return "正在操作";
        }
        if ("open_app".equals(kind)) {
            return "正在打开应用";
        }
        if ("observe".equals(kind)) {
            return "正在观察";
        }
        if ("apps".equals(kind)) {
            return "正在读取应用清单";
        }
        if ("shell".equals(kind)) {
            return "正在执行命令";
        }
        return "正在执行其他设备操作";
    }

    static String phaseSuffix(String phase) {
        String label = phaseLabel(phase);
        return "未知".equals(label) ? "" : "（" + label + "）";
    }

    /** 阶段只映射受控短码；未知阶段显示通用文案，不回显服务原文。 */
    static String phaseLabel(String phase) {
        if (!hasText(phase)) {
            return "未知";
        }
        switch (phase.trim()) {
            case "admitted": return "已接纳";
            case "dispatch": return "已派发";
            case "capture": return "采集中";
            case "observe": return "后置观察";
            case "running": return "运行中";
            case "finished": return "已结束";
            default: return "未知";
        }
    }

    /** 只读展示：已结束会话的简短摘要，不参与主文案、授权或准入。 */
    static String historySession(ServiceClient.Session session) {
        if (session == null) {
            return null;
        }
        StringBuilder summary = new StringBuilder(modeLabel(session.mode))
                .append(" · ").append(sessionStateLabel(session.state));
        if (hasText(session.agent)) {
            summary.append(" · ").append(session.agent.trim());
        }
        if (hasText(session.endedAt)) {
            summary.append(" · 结束 ").append(timeLabel(session.endedAt));
        }
        if (hasText(session.reason)) {
            summary.append(" · ").append(reasonLabel(session.reason.trim()));
        }
        return summary.toString();
    }

    /** 只读展示：最近一次设备工作摘要，不参与主文案、授权或准入。 */
    static String historyWork(ServiceClient.Work work) {
        if (work == null) {
            return null;
        }
        return workText(work.kind) + " · 阶段 " + phaseLabel(work.phase);
    }

    static String cleanupLabel(String value) {
        if ("complete".equals(value) || "confirmed".equals(value)) {
            return "已确认";
        }
        if ("not_applicable".equals(value)) {
            return "不适用";
        }
        if ("pending".equals(value) || "in_progress".equals(value)) {
            return "进行中";
        }
        if ("unconfirmed".equals(value) || "unknown".equals(value)) {
            return "未确认";
        }
        if ("failed".equals(value)) {
            return "失败";
        }
        return "未确认";
    }

    /** 只映射已知受控短码，未知值统一显示“其他原因”，不回显服务原文。 */
    static String reasonLabel(String reason) {
        if (reason == null) {
            return "其他原因";
        }
        switch (reason) {
            case "user_stop": return "用户停止";
            case "mode_changed": return "用户已切换屏幕模式";
            case "client_cancelled": return "Agent 主动取消";
            case "client_released": return "Agent 正常释放";
            case "lease_expired": return "会话租约到期";
            case "session_expired": return "会话已过期";
            case "session_closed": return "会话已释放";
            case "session_cancelled": return "会话已取消";
            case "session_failed": return "会话执行失败";
            case "control_unavailable": return "控制 App 不可用";
            case "device_unavailable": return "设备连接不可用";
            case "display_unavailable": return "目标屏幕不可用";
            case "display_changed": return "目标屏幕状态已变化";
            case "display_in_use": return "虚拟屏幕已被占用";
            case "display_command_timeout": return "虚拟屏幕命令超时";
            case "application_moved": return "目标应用已离开会话屏幕";
            case "control_ui_on_virtual_display": return "控制界面仅可驻留主屏";
            case "observation_failed": return "屏幕观察失败";
            case "action_result_uncertain": return "动作结果不确定";
            case "operation_cancelled": return "操作已取消";
            case "cleanup_pending": return "资源清理未完成";
            case "recovery_pending": return "恢复核验未完成";
            case "contract_mismatch": return "共同契约版本不匹配";
            default: return "其他原因";
        }
    }

    private static String reasonLine(String label, String reason) {
        return hasText(reason) ? "\n" + label + "：" + reasonLabel(reason.trim()) : "";
    }

    private static String reasonSuffix(String reason) {
        return hasText(reason) ? " · " + reasonLabel(reason.trim()) : "";
    }

    private static boolean hasText(String value) {
        return value != null && !value.trim().isEmpty();
    }

    /** 只用于服务公布时间的用户可读呈现；内容来自服务 RFC3339 文本，不重新解释语义。 */
    static String timeLabel(String value) {
        if (!hasText(value)) {
            return "—";
        }
        try {
            return Instant.parse(value.trim()).toString().replace('T', ' ').replace("Z", " UTC");
        } catch (RuntimeException invalidTime) {
            return "—";
        }
    }

    static String sequenceLabel(long seq) {
        return String.format(Locale.CHINA, "%d", seq);
    }
}
