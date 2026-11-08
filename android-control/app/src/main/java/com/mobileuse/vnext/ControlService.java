package com.mobileuse.vnext;

import android.Manifest;
import android.app.KeyguardManager;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.content.pm.ServiceInfo;
import android.content.res.Configuration;
import android.hardware.display.DisplayManager;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.PowerManager;
import android.os.SystemClock;
import android.util.DisplayMetrics;
import android.view.Display;

import java.io.File;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * API 36 specialUse 前台服务：拥有控制同步、用户屏幕选择、虚拟屏幕和用户停止状态。
 * 状态转换在主线程串行完成；网络、特权创屏及精确释放各有独立 worker，只回投原代次结果。
 * commandEpoch 在本地停止、停止确认、服务实例切换时递增，让已发出且不可撤回的旧响应失效。
 * 界面只消费服务投影的 control 快照与本机事实，不再保存第二份会话状态。
 */
public final class ControlService extends Service {
    static final String ACTION_START = "com.mobileuse.vnext.START";
    static final String ACTION_STOP = "com.mobileuse.vnext.STOP";
    static final String ACTION_REFRESH = "com.mobileuse.vnext.REFRESH";
    // 模式入口仅供本 App 的显式服务 Intent 使用；服务不导出，也不读取 Activity 的外部 extras。
    private static final String ACTION_SELECT_PRIMARY = "com.mobileuse.vnext.SELECT_PRIMARY";
    private static final String ACTION_SELECT_VIRTUAL = "com.mobileuse.vnext.SELECT_VIRTUAL";
    static final String CHANNEL_ID = "agent-control";
    /** Agent 消息通知使用独立频道，不复用停止/前台频道。 */
    static final String CHANNEL_ID_MESSAGES = "agent-messages";
    private static final String MESSAGE_GROUP = "agent-messages";
    private static final int MESSAGE_NOTIFICATION_ID = 1;
    private static final int MAX_NOTIFICATION_CODE_POINTS = 200;
    private static final int NOTIFICATION_ID = 78124;
    private static final String PREFERENCES = "control-lifecycle";
    // 停止意图跨进程重建保留：递增序号 + 原因，重建后仍按原意图重试。
    private static final String STOP_PENDING = "stop_pending";
    private static final String STOP_PENDING_REASON = "stop_pending_reason";
    private static final String STOP_SEQ = "stop_seq";

    private final Handler main = new Handler(Looper.getMainLooper());
    private final ExecutorService syncWorker = Executors.newSingleThreadExecutor(
            task -> new Thread(task, "control-sync"));
    // 用户停止绝不提交到 syncWorker；慢同步和普通命令都不能占用这条网络通道。
    private final ExecutorService stopWorker = Executors.newSingleThreadExecutor(
            task -> new Thread(task, "control-stop"));
    // 本实例已派发过的命令 ID；同 ID 重复投递不再执行，直到收到服务已移除该命令。
    private final Set<String> executedCommands = new HashSet<>();
    private final Map<String, ServiceClient.CommandResult> pendingResults = new LinkedHashMap<>();
    // 已成功持久化、等待服务释放的消息 id；每拍最多发送 32 条，成功响应后移除。
    private final LinkedHashSet<String> pendingMessageAcks = new LinkedHashSet<>();
    // 已写入内存但尚未确认落盘的消息；落盘成功后才确认并通知。
    private final Map<String, MessageList.Item> pendingPersistMessages = new LinkedHashMap<>();
    // 以下都由主线程拥有，并在发出同步时冻结副本；成功响应只清理本次实际携带的项。
    private final ControlApplication.MainActivityVisibilityListener mainActivityVisibilityListener =
            this::onMainActivityVisibility;
    private ControlApplication application;
    private ServiceClient client;
    private VirtualDisplayHost displays;
    private StatusOverlay overlay;
    private NotificationManager notifications;
    private PowerManager powerManager;
    private KeyguardManager keyguardManager;
    private PowerManager.WakeLock wakeLock;
    private boolean foreground;
    private boolean destroyed;
    private boolean syncInFlight;
    private boolean stopInFlight;
    private boolean stopPending;
    // 停止同步至多一个请求在途；失败按原意图同一 seq 重试，不每秒换新身份。
    private boolean stopDelivered;
    private long stopSeq;
    private String pendingStopReason = ServiceClient.STOP_REASON_USER;
    // 主屏授权只在本服务内存中存在；服务重建一律从虚拟屏开始，不写入 SharedPreferences。
    private String selectedMode = ServiceClient.MODE_VIRTUAL;
    private String modeGeneration = UUID.randomUUID().toString();
    private long commandEpoch;
    private String serviceInstanceId;
    private String stopError;
    private String displayError;
    private boolean connected;
    private long lastSyncElapsedMs = -1;
    private long lastRevision = -1;
    private ServiceClient.ControlView latestControl;
    private boolean failureReported;
    private String connectionTitle = "正在连接…";
    private String connectionDetail = "等待本机服务响应";
    private MessageStore messageStore;
    // 仅用于控制视图显示，不持久化、不参与主文案、授权或准入判断。
    private String lastEndedSessionSummary;
    private String lastObservedWorkSummary;

    private final Runnable tick = new Runnable() {
        @Override public void run() {
            if (destroyed) {
                return;
            }
            publishState();
            sendStop();
            // 不排积压请求；慢请求期间每秒刷新本地能力与失鲜状态，但最多只有一个同步在途。
            sendSync();
            main.postDelayed(this, 1000);
        }
    };

    @Override public void onCreate() {
        super.onCreate();
        application = (ControlApplication) getApplication();
        DiagnosticLog.initialize(this, application.instanceId);
        DiagnosticLog.event(DiagnosticLog.Event.STARTED, null, null);
        messageStore = application.messages();
        client = new ServiceClient(application.instanceId, GeneratedVersion.RELEASE_VERSION);
        notifications = getSystemService(NotificationManager.class);
        powerManager = getSystemService(PowerManager.class);
        keyguardManager = getSystemService(KeyguardManager.class);
        NotificationChannel channel = new NotificationChannel(CHANNEL_ID,
                "Agent 后台与停止", NotificationManager.IMPORTANCE_LOW);
        channel.setDescription("显示主屏共享 / 虚拟屏模式、后台运行及用户停止入口");
        channel.setShowBadge(false);
        channel.setLockscreenVisibility(Notification.VISIBILITY_PUBLIC);
        notifications.createNotificationChannel(channel);
        NotificationChannel messageChannel = new NotificationChannel(CHANNEL_ID_MESSAGES,
                getString(R.string.message_channel_name), NotificationManager.IMPORTANCE_DEFAULT);
        messageChannel.setDescription(getString(R.string.message_channel_description));
        messageChannel.setShowBadge(true);
        messageChannel.setLockscreenVisibility(Notification.VISIBILITY_PRIVATE);
        notifications.createNotificationChannel(messageChannel);
        SharedPreferences preferences = getSharedPreferences(PREFERENCES, MODE_PRIVATE);
        stopPending = preferences.getBoolean(STOP_PENDING, false);
        stopSeq = preferences.getLong(STOP_SEQ, 0);
        pendingStopReason = stopPending && ServiceClient.STOP_REASON_MODE_CHANGED.equals(
                preferences.getString(STOP_PENDING_REASON, null))
                ? ServiceClient.STOP_REASON_MODE_CHANGED : ServiceClient.STOP_REASON_USER;
        // 进程重建后同一 seq 可能已送达也可能未送达，因此重建后的待同步意图按同一序号重发；
        // 服务按原意图幂等处理，重复送达不会作用于新会话。
        stopDelivered = stopPending;
        startForeground(NOTIFICATION_ID, notification("虚拟屏 · 后台运行 · 等待连接"),
                ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        foreground = true;
        // FGS 本身不会阻止 CPU 休眠；锁屏时的每秒本机同步与停止重试需要这把部分唤醒锁。
        // 不亮屏、不持有主屏窗口，随服务销毁释放。
        wakeLock = powerManager.newWakeLock(
                PowerManager.PARTIAL_WAKE_LOCK, "MobileUse:control-sync");
        wakeLock.acquire();
        displays = new VirtualDisplayHost(this, main, application.instanceId, error -> {
            if (destroyed) return;
            displayError = error;
            publishState();
        });
        overlay = new StatusOverlay(this, () -> requestStop(ServiceClient.STOP_REASON_USER));
        application.subscribeMainActivityVisibility(mainActivityVisibilityListener);
        publishState();
        main.post(tick);
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent != null && RootDisplayProtocol.ACTION_ATTACH.equals(intent.getAction())) {
            String command = intent.getStringExtra(RootDisplayProtocol.EXTRA_COMMAND);
            String instance = intent.getStringExtra(RootDisplayProtocol.EXTRA_INSTANCE);
            IBinder endpoint = intent.getExtras() == null ? null
                    : intent.getExtras().getBinder(RootDisplayProtocol.EXTRA_ENDPOINT);
            if (command != null && instance != null && endpoint != null) {
                if (!destroyed && application.instanceId.equals(instance) && !stopPending
                        && ServiceClient.MODE_VIRTUAL.equals(selectedMode)) {
                    displays.connect(command, endpoint);
                } else {
                    // A bootstrap for an old App/STOP epoch cannot create resources. Close
                    // only its own capability, independently of the current display.
                    CompletableFuture.runAsync(() ->
                            RootDisplayConnection.closeEndpoint(endpoint, command, instance));
                }
            }
        } else if (intent != null && ACTION_STOP.equals(intent.getAction())) {
            requestStop(ServiceClient.STOP_REASON_USER);
        } else if (intent != null && ACTION_SELECT_PRIMARY.equals(intent.getAction())) {
            selectMode(ServiceClient.MODE_PRIMARY);
        } else if (intent != null && ACTION_SELECT_VIRTUAL.equals(intent.getAction())) {
            selectMode(ServiceClient.MODE_VIRTUAL);
        } else {
            // Activity 返回或授权通知后只刷新进程内状态；不会创建第二条网络轮询。
            publishState();
        }
        return START_STICKY;
    }

    /** Activity 只能提交明确的用户选择；动作值私有，外部 Intent 或 Agent 命令均不能授予主屏访问。 */
    static Intent selectModeIntent(Context context, String mode) {
        if (!ServiceClient.MODE_PRIMARY.equals(mode) && !ServiceClient.MODE_VIRTUAL.equals(mode)) {
            throw new IllegalArgumentException("不支持的屏幕模式");
        }
        return new Intent(context, ControlService.class).setAction(ServiceClient.MODE_PRIMARY.equals(mode)
                ? ACTION_SELECT_PRIMARY : ACTION_SELECT_VIRTUAL);
    }

    /** 主线程先换选择及代次，再走原有 STOP；确认前 mode_ready=false，旧同步由 epoch 拒绝。 */
    private void selectMode(String mode) {
        if (selectedMode.equals(mode)) {
            return;
        }
        selectedMode = mode;
        modeGeneration = UUID.randomUUID().toString();
        requestStop(ServiceClient.STOP_REASON_MODE_CHANGED);
    }

    /**
     * 主线程入口。先让在途响应失效并释放 VD，再发独立停止请求。
     * 停止期间收到的命令一律丢弃，只有该停止意图被受理后的新同步才可接收新命令。
     * STOP 不等待同步、设备工作或清理：这里只做本地失效与持久化，网络在专用 worker。
     */
    private void requestStop(String reason) {
        DiagnosticLog.event(DiagnosticLog.Event.STOP_REQUESTED, null, reason);
        commandEpoch++;
        displays.release();
        displayError = null;
        boolean alreadyPending = stopPending;
        stopPending = true;
        // STOP 同步期间再次请求时，用户停止覆盖模式切换；用户停止一经请求便保持最高优先级。
        pendingStopReason = alreadyPending
                && ServiceClient.STOP_REASON_USER.equals(pendingStopReason)
                ? ServiceClient.STOP_REASON_USER : reason;
        stopError = null;
        // 新意图必须取得新的进程内单调序号；只有本次运行中尚未送达的同一意图才可复用序号。
        if (!alreadyPending || stopDelivered) {
            stopSeq++;
            DiagnosticLog.event(DiagnosticLog.Event.STOP_SEQ_ADVANCED, null, null);
        }
        stopDelivered = false;
        // 即使进程在停止同步失败后被系统重建，也必须继续同步停止，不能重新创建旧 VD。
        // 保存失败不回滚停止；内存中的待同步意图仍生效，并阻断新准入。
        if (!getSharedPreferences(PREFERENCES, MODE_PRIVATE).edit()
                .putBoolean(STOP_PENDING, true)
                .putString(STOP_PENDING_REASON, pendingStopReason)
                .putLong(STOP_SEQ, stopSeq)
                .commit()) {
            stopError = "停止意图保存失败；本次运行仍保持本地停止，请恢复存储后再重启后台。";
        }
        publishState();
        sendStop();
    }

    private void sendSync() {
        if (syncInFlight || !foreground) {
            return;
        }
        syncInFlight = true;
        long requestEpoch = commandEpoch;
        boolean visible = notificationsEnabled();
        Boolean powerInteractive = powerManager == null ? null : powerManager.isInteractive();
        Boolean keyguardLocked = keyguardManager == null ? null : keyguardManager.isKeyguardLocked();
        ServiceClient.SyncRequest request = new ServiceClient.SyncRequest(selectedMode, modeGeneration,
                !stopPending && !displays.cleanupPending(), foreground && visible, visible, foreground,
                powerInteractive, keyguardLocked, primaryDisplay(), displays.descriptor(),
                new ArrayList<>(pendingResults.values()), messageAcksForRequest(),
                stopPending ? stopSeq : null, pendingStopReason);
        syncWorker.execute(() -> {
            try {
                ConnectionConfig config = loadConfig();
                ServiceClient.SyncResponse response = client.sync(config, request);
                main.post(() -> finishSync(requestEpoch, request, response));
            } catch (ConnectionConfig.ConfigException error) {
                main.post(() -> networkFailure(requestEpoch, "连接配置无效", error.getMessage()));
            } catch (ServiceClient.ServiceException error) {
                main.post(() -> networkFailure(requestEpoch, error.title, error.getMessage()));
            }
        });
    }

    private void finishSync(long requestEpoch, ServiceClient.SyncRequest request,
            ServiceClient.SyncResponse response) {
        syncInFlight = false;
        // 成功响应说明服务已处理本拍请求；已发送的确认可移除，传输失败路径不会到这里。
        for (String id : request.messageAcks) {
            pendingMessageAcks.remove(id);
        }
        if (destroyed || requestEpoch != commandEpoch) {
            DiagnosticLog.event(DiagnosticLog.Event.RESPONSE_DISCARDED, response.requestId,
                    destroyed ? "service_destroyed" : "epoch_changed");
            return;
        }
        boolean instanceChanged = acceptSync(response);
        // 消息与执行权威无关；服务实例变化时仍保留 App 私有消息并继续确认。
        mergeMessages(response.messages);
        // 响应内新执行的命令还未上报；只能确认发出请求时已带上的那一批结果。
        for (ServiceClient.CommandResult sent : request.results) {
            pendingResults.remove(sent.id, sent);
        }
        if (response.stop != null && stopPending && response.stop.seq == stopSeq
                && ServiceClient.isStopState(response.stop.state)) {
            // 本拍携带了 stop_pending 且服务确认了同一序号：停止边界已绑定。
            confirmStop(requestEpoch);
        }
        // 实例变化的第一拍只完成恢复握手：待下一拍同步上报本实例的 display 事实后再执行命令。
        if (!instanceChanged && !stopPending) {
            for (ServiceClient.Command command : response.commands) {
                execute(command);
            }
        } else if (!response.commands.isEmpty()) {
            DiagnosticLog.event(DiagnosticLog.Event.RESPONSE_DISCARDED, response.requestId,
                    instanceChanged ? "instance_changed" : "stop_pending");
        }
        publishState();
    }

    /** 冻结本拍要发送的确认，最多 32 条；未发送的留在队列中由后续拍次继续。 */
    private List<String> messageAcksForRequest() {
        ArrayList<String> acks = new ArrayList<>(Math.min(
                pendingMessageAcks.size(), ServiceClient.MAX_MESSAGE_ACKS));
        for (String id : pendingMessageAcks) {
            if (acks.size() == ServiceClient.MAX_MESSAGE_ACKS) {
                break;
            }
            acks.add(id);
        }
        return acks;
    }

    /**
     * 把本次响应的消息并入 App 私有存储。只有落盘成功后才确认并通知；
     * 落盘失败时保留待处理集合，下一次成功同步继续尝试，不产生服务端已读语义。
     */
    private void mergeMessages(List<ServiceClient.Message> delivered) {
        if (messageStore == null) {
            return;
        }
        ArrayList<String> deliveredIds = new ArrayList<>();
        if (delivered != null && !delivered.isEmpty()) {
            String receivedAt = MessageStore.receivedAtNow();
            ArrayList<MessageList.Item> incoming = new ArrayList<>(delivered.size());
            for (ServiceClient.Message message : delivered) {
                incoming.add(new MessageList.Item(message.id, message.seq, message.text,
                        message.createdAt, false, receivedAt));
                deliveredIds.add(message.id);
            }
            for (MessageList.Item item : messageStore.merge(incoming)) {
                pendingPersistMessages.put(item.id, item);
            }
        }
        flushMessagePersistence();
        // 服务会重发未确认消息。App 重启后内存确认队列为空，已落盘的重复投递也必须确认，
        // 否则服务会一直重发；尚未落盘的新消息仍由 pendingPersistMessages 挡住。
        for (String id : deliveredIds) {
            if (messageStore.find(id) != null && !pendingPersistMessages.containsKey(id)) {
                pendingMessageAcks.add(id);
            }
        }
    }

    private void flushMessagePersistence() {
        if (messageStore == null || pendingPersistMessages.isEmpty()) {
            return;
        }
        if (!messageStore.persist()) {
            return;
        }
        for (MessageList.Item item : new ArrayList<>(pendingPersistMessages.values())) {
            if (messageStore.find(item.id) == null) {
                continue;
            }
            pendingMessageAcks.add(item.id);
            notifyMessage(item);
        }
        pendingPersistMessages.clear();
    }

    /** 每条新消息一条独立通知；频道/权限不可用时静默跳过，正文不进入日志。 */
    private void notifyMessage(MessageList.Item message) {
        if (!notificationsEnabled(this, CHANNEL_ID_MESSAGES)) {
            return;
        }
        String text = MessageList.oneLineSnippet(message.text, MAX_NOTIFICATION_CODE_POINTS);
        Intent open = PrimaryDisplayUi.controlIntent(this)
                .setAction(MainActivity.ACTION_OPEN_MESSAGE)
                .putExtra(MainActivity.EXTRA_MESSAGE_ID, message.id)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        PendingIntent pending = PendingIntent.getActivity(this,
                100000 + Math.floorMod(message.id.hashCode(), 100000), open,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE,
                PrimaryDisplayUi.options());
        Notification notification = new Notification.Builder(this, CHANNEL_ID_MESSAGES)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentTitle(getString(R.string.message_notification_title))
                .setContentText(text)
                .setStyle(new Notification.BigTextStyle().bigText(text))
                .setContentIntent(pending)
                .setAutoCancel(true)
                .setGroup(MESSAGE_GROUP)
                .setCategory(Notification.CATEGORY_MESSAGE)
                .setVisibility(Notification.VISIBILITY_PRIVATE)
                .build();
        try {
            notifications.notify(message.id, MESSAGE_NOTIFICATION_ID, notification);
        } catch (SecurityException unavailableAfterCheck) {
            // 检查后权限被撤销：跳过本条通知，消息仍保存在 App 私有存储中。
        }
    }

    private void sendStop() {
        if (stopInFlight || !foreground) {
            return;
        }
        stopInFlight = true;
        long requestEpoch = commandEpoch;
        long requestSeq = stopSeq;
        String requestReason = pendingStopReason;
        stopWorker.execute(() -> {
            try {
                ServiceClient.StopAck ack = client.stop(loadConfig(), requestSeq, requestReason);
                main.post(() -> finishStop(requestEpoch, requestSeq, ack));
            } catch (ConnectionConfig.ConfigException error) {
                main.post(() -> stopFailure(requestEpoch, requestSeq, "连接配置无效", error.getMessage()));
            } catch (ServiceClient.ServiceException error) {
                main.post(() -> stopFailure(requestEpoch, requestSeq, error.title, error.getMessage()));
            }
        });
    }

    /**
     * 只有同一 App 实例、同一停止序号且被受理的确认才清除 pending。服务实例可以不同：
     * 停止意图不因服务重启而作废，确认仍属于原意图。
     */
    private void finishStop(long requestEpoch, long requestSeq, ServiceClient.StopAck ack) {
        stopInFlight = false;
        if (destroyed) {
            return;
        }
        if (!ack.accepted || ack.stopSeq != requestSeq) {
            stopError = "服务未受理本次停止，正在重试。";
            publishState();
            return;
        }
        if (requestEpoch != commandEpoch || requestSeq != stopSeq || !stopPending) {
            // 停止已在本地被更新的意图覆盖；这一确认不再清除当前 pending。
            DiagnosticLog.event(DiagnosticLog.Event.RESPONSE_DISCARDED, null, "stop_superseded");
            publishState();
            return;
        }
        DiagnosticLog.event(DiagnosticLog.Event.STOP_CONFIRMED, null, null);
        // 迟到但被受理的确认仍证明服务已绑定该停止边界；记录后不再改动本地当前意图。
        publishState();
    }

    private void confirmStop(long requestEpoch) {
        stopPending = false;
        stopError = null;
        pendingStopReason = ServiceClient.STOP_REASON_USER;
        stopDelivered = true;
        // 使停止确认之前发出的同步失效，不能在确认后再接受它携带的旧 create。
        commandEpoch = Math.max(commandEpoch, requestEpoch + 1);
        getSharedPreferences(PREFERENCES, MODE_PRIVATE).edit()
                .putBoolean(STOP_PENDING, false)
                .putString(STOP_PENDING_REASON, ServiceClient.STOP_REASON_USER)
                .putLong(STOP_SEQ, stopSeq)
                .apply();
    }

    private void stopFailure(long requestEpoch, long requestSeq, String title, String detail) {
        stopInFlight = false;
        if (destroyed || requestSeq != stopSeq) {
            return;
        }
        // stopPending 不清除；每秒按同一意图重试，本地 VD 始终保持释放。
        stopError = title + "：" + detail;
        publishState();
    }

    /** 每次请求重读配置，部署或换凭据无需重启 Activity/服务。 */
    private ConnectionConfig loadConfig() throws ConnectionConfig.ConfigException {
        ConnectionConfig config = ConnectionConfig.load(new File(getFilesDir(), "connection.json"));
        if (config == null) {
            throw new ConnectionConfig.ConfigException("尚未配置连接；请由部署流程写入 files/connection.json。");
        }
        return config;
    }

    private void networkFailure(long requestEpoch, String title, String detail) {
        syncInFlight = false;
        if (destroyed || requestEpoch != commandEpoch) {
            return;
        }
        if (connected || !failureReported) {
            DiagnosticLog.event(DiagnosticLog.Event.DISCONNECTED, null, "control_request_failed");
            failureReported = true;
        }
        connected = false;
        connectionTitle = title;
        connectionDetail = detail;
        // 失鲜后界面必须进入未知；不保留缓存 active 作为当前事实。
        lastSyncElapsedMs = -1;
        latestControl = null;
        publishState();
    }

    /**
     * 接受一次成功同步。返回 true 表示服务实例切换：旧命令/回执/视图失效，
     * 但停止意图保留；本拍不执行任何命令，下一拍先上报本实例实际 display。
     */
    private boolean acceptSync(ServiceClient.SyncResponse response) {
        boolean changed = serviceInstanceId != null
                && !serviceInstanceId.equals(response.serviceInstanceId);
        if (changed) {
            DiagnosticLog.event(DiagnosticLog.Event.INSTANCE_CHANGED, response.serviceInstanceId, null);
            commandEpoch++;
            displays.release();
            executedCommands.clear();
            pendingResults.clear();
            displayError = "本机服务实例已更换，旧虚拟屏幕与命令已清除；等待新的恢复结果。";
            lastRevision = -1;
            latestControl = null;
            // App 私有消息与已读状态不随服务实例切换清除；确认队列仍可继续发送。
        }
        serviceInstanceId = response.serviceInstanceId;
        // 同实例 revision 单调；旧/重复视图不得覆盖新的当前事实。实例切换后以新视图重新起算。
        if (changed || latestControl == null || response.control.revision > lastRevision) {
            latestControl = response.control;
            lastRevision = response.control.revision;
            // 历史摘要只用于展示，永远不参与主文案、授权或准入。
            if (response.control.session != null
                    && "ended".equals(response.control.session.state)) {
                lastEndedSessionSummary = ControlState.historySession(response.control.session);
            }
            if (response.control.work != null) {
                lastObservedWorkSummary = ControlState.historyWork(response.control.work);
            }
            DiagnosticLog.event(DiagnosticLog.Event.CONTROL_UPDATED, response.serviceInstanceId,
                    response.control.availability.state, -1, -1, null);
        }
        if (!connected) {
            DiagnosticLog.event(DiagnosticLog.Event.CONNECTED, response.serviceInstanceId, null);
        }
        failureReported = false;
        connected = true;
        lastSyncElapsedMs = SystemClock.elapsedRealtime();
        connectionTitle = "已连接 · 共同契约 " + ServiceClient.CONTRACT_VERSION;
        connectionDetail = "控制同步已确认（" + response.control.availability.state + "）";
        return changed;
    }

    /**
     * 主线程准入：只在契约精确匹配、无本地停止、无未确认清理、模式就绪且服务视图可用时
     * 执行显示命令。任一条件不满足都回执明确失败，但不伪造资源状态。
     */
    private void execute(ServiceClient.Command command) {
        if (command == null || command.id == null) {
            return;
        }
        if (!executedCommands.add(command.id)) {
            return;
        }
        DiagnosticLog.event(DiagnosticLog.Event.COMMAND_START, command.id, null);
        String error = null;
        try {
            if (stopPending) {
                throw new IllegalStateException("local_stop_pending");
            }
            // 创建新屏需要完整 ready；关闭原屏是收尾通道，只要控制视图仍新鲜即可执行，
            // 否则 recovering/清理中的旧资源将永远无法被精确释放。
            if (command.createsDisplay()) {
                if (!freshControlAvailable(SystemClock.elapsedRealtime())) {
                    throw new IllegalStateException("control_not_ready");
                }
                if (!ServiceClient.MODE_VIRTUAL.equals(selectedMode)) {
                    throw new IllegalStateException("mode_not_virtual");
                }
                if (!foreground || !notificationsEnabled()) {
                    throw new IllegalStateException("stop_channel_unavailable");
                }
                long epoch = commandEpoch;
                displays.create(command.id, command.width, command.height, command.densityDpi,
                        result -> finishDisplayCommand(command, epoch, true, result));
                return;
            }
            if (!freshControlView(SystemClock.elapsedRealtime())) {
                throw new IllegalStateException("control_stale");
            }
            // close_display 只回收命令精确指向的、本实例持有的那个屏幕。缺少目标时拒绝执行，
            // 绝不回退为“释放当前任意屏幕”，以免误伤后来创建的新 generation。
            if (command.targetAppInstanceId == null || command.targetDisplayId == null
                    || command.targetDisplayId <= 0 || command.targetGeneration == null) {
                throw new IllegalArgumentException("close_display_target_missing");
            }
            if (application.instanceId.equals(command.targetAppInstanceId)) {
                long epoch = commandEpoch;
                displays.releaseTarget(command.targetDisplayId, command.targetGeneration,
                        result -> finishDisplayCommand(command, epoch, false, result));
                return;
            }
            // 更早 App 实例的资源归属已随其实例失效；不得触碰本实例的屏幕，
            // 仅回执失败，由服务按其后事实核验物理释放。
            throw new IllegalStateException("close_display_foreign_instance");
        } catch (IllegalArgumentException | IllegalStateException | SecurityException
                | UnsupportedOperationException failure) {
            // 设备调用边界：上报受控原因码，保留实际 descriptor，不伪造成功或重建。
            error = failure.getMessage() == null ? "display_command_failed" : failure.getMessage();
            displayError = displayErrorText(error);
        }
        pendingResults.put(command.id, new ServiceClient.CommandResult(command.id, error == null, error));
        DiagnosticLog.event(DiagnosticLog.Event.COMMAND_END, command.id,
                error == null ? null : "display_command_failed");
    }

    /** 控制视图必须新鲜且服务报告可用，才允许任何显示副作用。 */
    private boolean freshControlAvailable(long now) {
        return connected && latestControl != null
                && lastSyncElapsedMs >= 0
                && now - lastSyncElapsedMs <= ControlState.STALE_AFTER_MS
                && "ready".equals(latestControl.availability.state);
    }

    /** 收尾命令只要求控制视图新鲜；availability 可以处于 recovering/blocked。 */
    private boolean freshControlView(long now) {
        return connected && latestControl != null
                && lastSyncElapsedMs >= 0
                && now - lastSyncElapsedMs <= ControlState.STALE_AFTER_MS;
    }

    private static String displayErrorText(String code) {
        switch (code) {
            case "display_cleanup_unconfirmed": return "上一块虚拟屏清理尚未确认，暂不创建新屏。";
            case "display_create_in_progress": return "虚拟屏正在创建，等待确认。";
            case "display_create_timeout": return "虚拟屏创建超时，已按未确认处理。";
            case "display_create_cancelled": return "虚拟屏创建已被停止取消。";
            case "display_create_unconfirmed": return "虚拟屏创建未被系统确认。";
            case "display_release_identity_unknown": return "释放目标的显示屏身份不在本 App 记录中。";
            case "local_stop_pending": return "本地停止待同步，已拒绝旧命令。";
            case "control_not_ready": return "服务控制尚未就绪，已拒绝显示命令。";
            case "mode_not_virtual": return "当前已选择主屏共享，不能创建虚拟屏幕。";
            case "stop_channel_unavailable": return "通知停止入口不可见，已拒绝创建虚拟屏。";
            case "close_display_target_missing": return "释放命令缺少目标屏幕身份，已拒绝执行。";
            case "close_display_foreign_instance": return "释放目标属于旧 App 实例，已拒绝触碰本实例屏幕。";
            default: return "显示命令失败（" + code + "）。";
        }
    }

    /** 异步完成只对原命令代次权威；迟到结果不能恢复运行态、发布成功或操作更新的屏幕。 */
    private void finishDisplayCommand(ServiceClient.Command command, long epoch,
            boolean created, String error) {
        if (destroyed || epoch != commandEpoch || stopPending) {
            DiagnosticLog.event(DiagnosticLog.Event.COMMAND_END, command.id, "display_command_cancelled");
            // 仍按原命令回执失败：本地处理已取消，不能留给服务一个永远不回执的命令。
            if (!destroyed) {
                pendingResults.put(command.id, new ServiceClient.CommandResult(
                        command.id, false, "display_command_cancelled"));
            }
            return;
        }
        displayError = error == null ? null : displayErrorText(error);
        pendingResults.put(command.id, new ServiceClient.CommandResult(command.id, error == null, error));
        DiagnosticLog.event(DiagnosticLog.Event.COMMAND_END, command.id,
                error == null ? null : "display_command_failed");
        publishState();
    }

    /** 权限、全局开关及本频道都必须可见，FGS 单独运行不代表用户能从通知停止。 */
    private boolean notificationsEnabled() {
        return notificationsEnabled(this, CHANNEL_ID);
    }

    /** 共用同一可观测判断，避免仅因常驻通知频道开启就误报停止可用。 */
    static boolean notificationsEnabled(Context context, String channelId) {
        NotificationManager manager = context.getSystemService(NotificationManager.class);
        NotificationChannel channel = manager.getNotificationChannel(channelId);
        return context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)
                == PackageManager.PERMISSION_GRANTED
                && manager.areNotificationsEnabled() && channel != null
                && channel.getImportance() != NotificationManager.IMPORTANCE_NONE;
    }

    /**
     * 只读取当前主屏，不创建/拥有它。API 36 的 getDisplays() 默认排除 disabled display，
     * 再要求 DEFAULT_DISPLAY 有效且 STATE_ON；锁屏熄屏/禁用时上报 null，不沿用旧尺寸。
     * 主屏仅复用 VD 描述符的数据格式；generation 对应用户选择代次，旋转不更换授权代次。
     */
    @SuppressWarnings("deprecation")
    private VirtualDisplayHost.Descriptor primaryDisplay() {
        for (Display current : getSystemService(DisplayManager.class).getDisplays()) {
            if (current.getDisplayId() == Display.DEFAULT_DISPLAY) {
                if (!current.isValid() || current.getState() != Display.STATE_ON) {
                    return null;
                }
                DisplayMetrics metrics = new DisplayMetrics();
                current.getRealMetrics(metrics);
                return new VirtualDisplayHost.Descriptor(Display.DEFAULT_DISPLAY, metrics.widthPixels,
                        metrics.heightPixels, metrics.densityDpi, current.getRotation(), modeGeneration);
            }
        }
        return null;
    }

    private void publishState() {
        if (destroyed) {
            return;
        }
        long now = SystemClock.elapsedRealtime();
        // 失鲜计时按发布时刻推进，不依赖响应回调；因此没有网络时界面也会进入未知。
        long age = lastSyncElapsedMs < 0 ? -1 : now - lastSyncElapsedMs;
        boolean fresh = connected && age >= 0 && age <= ControlState.STALE_AFTER_MS;
        // 生命周期回调之外，每次发布也重读最新 Activity 可见性，覆盖服务/Activity 的任意启动先后。
        overlay.setTemporarilyHidden(application.mainActivityVisible());
        boolean visible = notificationsEnabled();
        overlay.refreshPermission();
        VirtualDisplayHost.Descriptor display = displays.descriptor();
        // 持有当前会话的虚拟屏不是清理风险；只有尚未确认释放的旧尝试才算本地风险。
        boolean cleanupPending = displays.cleanupPending();
        int unreadMessages = messageStore == null ? 0 : messageStore.unreadCount();
        ControlState snapshot = new ControlState(foreground, visible, overlay.ready(), overlay.visible(),
                overlay.issue(), foreground && visible, stopPending, stopSeq, stopError,
                selectedMode, primaryDisplay(), display, displayError, cleanupPending,
                fresh, fresh ? age : -1, connectionTitle, connectionDetail, serviceInstanceId,
                fresh ? latestControl : null, unreadMessages,
                lastEndedSessionSummary, lastObservedWorkSummary);
        application.publish(snapshot);
        overlay.render(snapshot);
        // 前台通知与 Activity、悬浮窗共用同一快照主文案。
        notifications.notify(NOTIFICATION_ID, notification(snapshot.summary()));
    }

    /** Activity 生命周期变化时立即切换窗口，并由同一次发布同步界面与通知文案。 */
    private void onMainActivityVisibility(boolean visible) {
        if (destroyed || overlay == null) {
            return;
        }
        overlay.setTemporarilyHidden(visible);
        publishState();
    }

    @Override public void onConfigurationChanged(Configuration configuration) {
        super.onConfigurationChanged(configuration);
        overlay.reposition();
    }

    private Notification notification(String text) {
        Intent open = PrimaryDisplayUi.controlIntent(this)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        PendingIntent openApp = PendingIntent.getActivity(this, 0, open,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE, PrimaryDisplayUi.options());
        PendingIntent stop = PendingIntent.getService(this, 1,
                new Intent(this, ControlService.class).setAction(ACTION_STOP),
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        Notification notification = new Notification.Builder(this, CHANNEL_ID)
                .setSmallIcon(R.drawable.ic_notification).setContentTitle("Mobile Use 后台控制")
                .setContentText(text).setContentIntent(openApp)
                .setStyle(new Notification.BigTextStyle().bigText(text))
                .setCategory(Notification.CATEGORY_SERVICE).setVisibility(Notification.VISIBILITY_PUBLIC)
                .setOngoing(true).setOnlyAlertOnce(true)
                .setForegroundServiceBehavior(Notification.FOREGROUND_SERVICE_IMMEDIATE)
                .addAction(new Notification.Action.Builder(null, "停止操作", stop).build()).build();
        // 当前小米 SystemUI 用此 extras 开关控制标准 action 行；不设时连展开通知
        // 也隐藏停止按钮。仍使用同一个不可变 STOP PendingIntent，不增加停止通道。
        notification.extras.putBoolean("miui.showAction", true);
        return notification;
    }

    @Override public void onDestroy() {
        DiagnosticLog.event(DiagnosticLog.Event.STOPPED, null, null);
        // 先阻断后续状态/命令回调。HTTP 已发出不能假定撤回，迟到响应只会被丢弃。
        destroyed = true;
        foreground = false;
        commandEpoch++;
        application.unsubscribeMainActivityVisibility(mainActivityVisibilityListener);
        main.removeCallbacksAndMessages(null);
        overlay.close();
        displays.close();
        client.close();
        syncWorker.shutdownNow();
        stopWorker.shutdownNow();
        if (wakeLock.isHeld()) {
            wakeLock.release();
        }
        stopForeground(STOP_FOREGROUND_REMOVE);
        application.publish(new ControlState(false, notificationsEnabled(), false, false, null, false,
                stopPending, stopSeq, stopError, selectedMode, null, null, null, true,
                false, -1, "后台已结束",
                "重新启动后台后恢复同步，并默认使用虚拟屏。", null, null,
                messageStore == null ? 0 : messageStore.unreadCount(), null, null));
        super.onDestroy();
    }

    @Override public IBinder onBind(Intent intent) {
        return null;
    }
}
