package com.mobileuse.probe;

import android.app.Activity;
import android.os.Bundle;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * 受控第二页：既可作为同一任务内的页面栈，也可由父页面以 FLAG_ACTIVITY_NEW_TASK
 * 加上独立 taskAffinity 启动为第二任务。只展示固定中文文本与关闭入口，不保存状态。
 */
public final class ProbeSecondActivity extends Activity {
    @Override protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(60, 160, 60, 60);

        TextView title = new TextView(this);
        title.setText("mobile-use probe · 第二页");
        title.setTextSize(26);

        TextView body = new TextView(this);
        body.setText("第二页 · 受控页面栈\n用于验证同任务页面栈与独立第二任务。");
        body.setTextSize(20);

        TextView status = new TextView(this);
        status.setText("状态：第二页已打开");
        status.setTextSize(20);

        Button close = new Button(this);
        close.setText("关闭此页");
        close.setTextSize(18);
        close.setOnClickListener(view -> finish());

        append(root, title);
        append(root, body);
        append(root, status);
        append(root, close);
        setContentView(root);
    }

    private static void append(LinearLayout root, View view) {
        root.addView(view, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
    }
}
