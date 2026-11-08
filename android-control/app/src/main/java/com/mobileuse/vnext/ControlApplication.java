package com.mobileuse.vnext;

import android.app.Application;

import java.util.LinkedHashSet;
import java.util.Set;
import java.util.UUID;

/**
 * 保存进程身份、最近一次不可变快照和 MainActivity 的可见生命周期，不持有网络线程或设备资源。
 * 所有订阅、发布都在主线程；Activity 重建只更新订阅，不会新增网络轮询。
 */
public final class ControlApplication extends Application {
    final String instanceId = UUID.randomUUID().toString();
    private final Set<Listener> listeners = new LinkedHashSet<>();
    private final Set<MainActivityVisibilityListener> visibilityListeners = new LinkedHashSet<>();
    // onStart 加入、onStop 删除独立 Activity token；配置重建时旧 onStop 不会覆盖新 onStart。
    private final Set<Object> visibleMainActivities = new LinkedHashSet<>();
    private ControlState state = ControlState.inactive();

    interface Listener {
        void onControlState(ControlState state);
    }

    interface MainActivityVisibilityListener {
        void onMainActivityVisibility(boolean visible);
    }

    ControlState state() {
        return state;
    }

    void subscribe(Listener listener) {
        listeners.add(listener);
        listener.onControlState(state);
    }

    void unsubscribe(Listener listener) {
        listeners.remove(listener);
    }

    void publish(ControlState latest) {
        state = latest;
        for (Listener listener : listeners.toArray(new Listener[0])) {
            listener.onControlState(latest);
        }
    }

    /**
     * 完整控制界面是否可见只存在于当前进程。每个 Activity 实例使用独立 token；
     * 集合在最后一个 started Activity 停止后才变为空，因此可容忍重建期间的旧 onStop。
     */
    void reportMainActivityVisibility(Object activityIdentity, boolean visible) {
        boolean changed = visible
                ? visibleMainActivities.add(activityIdentity)
                : visibleMainActivities.remove(activityIdentity);
        if (!changed) {
            return;
        }
        boolean current = mainActivityVisible();
        for (MainActivityVisibilityListener listener
                : visibilityListeners.toArray(new MainActivityVisibilityListener[0])) {
            listener.onMainActivityVisibility(current);
        }
    }

    boolean mainActivityVisible() {
        return !visibleMainActivities.isEmpty();
    }

    /**
     * Service 当前是唯一订阅者。订阅后执行一次状态发布即可取得当前值；
     * 之后的 onStart/onStop 变化由回调推送，不存在静态 Service 引用或轮询。
     */
    void subscribeMainActivityVisibility(MainActivityVisibilityListener listener) {
        visibilityListeners.add(listener);
    }

    void unsubscribeMainActivityVisibility(MainActivityVisibilityListener listener) {
        visibilityListeners.remove(listener);
    }
}
