package com.mobileuse.probe;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.media.session.MediaSession;
import android.media.session.PlaybackState;
import android.os.IBinder;

/**
 * 受控前台服务与 MediaSession：只由测试页面按钮显式起停，不播放真实音频、不读取
 * 用户数据。启动后保持 STATE_PLAYING 与 mediaPlayback 前台类型，便于观察
 * dumpsys media_session 的播放态和 dumpsys activity services 的前台事实。
 *
 * 未授予通知权限时系统只隐藏通知，前台服务与播放态仍可观察；这里不弹运行时授权，
 * 以免权限对话框遮挡受控页面。
 */
public final class ProbePlaybackService extends Service {
    static final String ACTION_START = "com.mobileuse.probe.action.START_PLAYBACK";
    static final String ACTION_STOP = "com.mobileuse.probe.action.STOP_PLAYBACK";
    private static final String CHANNEL_ID = "probe-playback";
    private static final int NOTIFICATION_ID = 4211;

    private MediaSession session;

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        String action = intent == null ? null : intent.getAction();
        if (ACTION_STOP.equals(action)) {
            stopPlayback();
            return START_NOT_STICKY;
        }
        startPlayback();
        return START_NOT_STICKY;
    }

    private void startPlayback() {
        if (session == null) {
            session = new MediaSession(this, "MobileUseProbePlayback");
            session.setFlags(MediaSession.FLAG_HANDLES_MEDIA_BUTTONS
                    | MediaSession.FLAG_HANDLES_TRANSPORT_CONTROLS);
            session.setPlaybackState(new PlaybackState.Builder()
                    .setActions(PlaybackState.ACTION_PLAY | PlaybackState.ACTION_PAUSE
                            | PlaybackState.ACTION_STOP)
                    .setState(PlaybackState.STATE_PLAYING, 0L, 1.0f)
                    .build());
            session.setActive(true);
        }
        ensureChannel();
        startForeground(NOTIFICATION_ID, buildNotification(),
                ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK);
    }

    private void stopPlayback() {
        if (session != null) {
            session.setPlaybackState(new PlaybackState.Builder()
                    .setState(PlaybackState.STATE_STOPPED, 0L, 0f)
                    .build());
            session.setActive(false);
            session.release();
            session = null;
        }
        stopForeground(STOP_FOREGROUND_REMOVE);
        stopSelf();
    }

    private void ensureChannel() {
        NotificationManager manager = getSystemService(NotificationManager.class);
        if (manager != null && manager.getNotificationChannel(CHANNEL_ID) == null) {
            manager.createNotificationChannel(new NotificationChannel(CHANNEL_ID,
                    "探针后台播放", NotificationManager.IMPORTANCE_LOW));
        }
    }

    private Notification buildNotification() {
        return new Notification.Builder(this, CHANNEL_ID)
                .setSmallIcon(android.R.drawable.ic_media_play)
                .setContentTitle("探针后台播放")
                .setContentText("受控 MediaSession · 前台服务")
                .setOngoing(true)
                .build();
    }

    @Override public void onDestroy() {
        if (session != null) {
            session.setActive(false);
            session.release();
            session = null;
        }
        super.onDestroy();
    }

    @Override public IBinder onBind(Intent intent) {
        return null;
    }
}
