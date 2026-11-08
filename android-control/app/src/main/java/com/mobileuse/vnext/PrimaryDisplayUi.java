package com.mobileuse.vnext;

import android.app.Activity;
import android.app.ActivityOptions;
import android.content.Context;
import android.content.Intent;
import android.os.Bundle;
import android.view.Display;

/** 控制界面的唯一启动契约；后台 Service/VD 不使用此 UI 约束。 */
final class PrimaryDisplayUi {
    private PrimaryDisplayUi() {}

    static Bundle options() {
        return ActivityOptions.makeBasic().setLaunchDisplayId(Display.DEFAULT_DISPLAY).toBundle();
    }

    static Intent controlIntent(Context context) {
        return new Intent(context, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK
                | Intent.FLAG_ACTIVITY_CLEAR_TOP | Intent.FLAG_ACTIVITY_SINGLE_TOP);
    }

    /** 即使调用来源的焦点/Context 属于 VD，界面也只可落在用户主屏。 */
    static void open(Context context, Intent intent) {
        context.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK
                | Intent.FLAG_ACTIVITY_CLEAR_TOP | Intent.FLAG_ACTIVITY_SINGLE_TOP), options());
    }

    static boolean isPrimary(Activity activity) {
        Display display = activity.getDisplay();
        return display != null && display.getDisplayId() == Display.DEFAULT_DISPLAY;
    }
}
