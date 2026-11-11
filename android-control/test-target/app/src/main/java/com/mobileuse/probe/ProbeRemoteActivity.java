package com.mobileuse.probe;

import android.app.Activity;
import android.os.Bundle;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * 受控远程进程页面：在 :remote 进程内运行，并带独立 taskAffinity，因此既展示
 * 跨进程任务，也可作为第二任务被认领/交还验证。只显示固定中文文本与本进程名。
 */
public final class ProbeRemoteActivity extends Activity {
    @Override protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(60, 160, 60, 60);

        TextView title = new TextView(this);
        title.setText("mobile-use probe · 远程进程");
        title.setTextSize(26);

        TextView process = new TextView(this);
        process.setText("进程：" + getApplication().getProcessName());
        process.setTextSize(20);

        TextView status = new TextView(this);
        status.setText("状态：远程进程页面已打开");
        status.setTextSize(20);

        Button close = new Button(this);
        close.setText("关闭此页");
        close.setTextSize(18);
        close.setOnClickListener(view -> finish());

        append(root, title);
        append(root, process);
        append(root, status);
        append(root, close);
        setContentView(root);
    }

    private static void append(LinearLayout root, View view) {
        root.addView(view, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
    }
}
