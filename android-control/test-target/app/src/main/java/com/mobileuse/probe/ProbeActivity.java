package com.mobileuse.probe;

import android.app.Activity;
import android.content.Intent;
import android.os.Bundle;
import android.text.InputType;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowManager;
import android.widget.Button;
import android.widget.CheckBox;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * Controlled verification target for the writable loop. It exposes a text
 * label, one editable field, one clickable button with a visible counter and a
 * checkbox so a public tree can be observed and a Chinese text write can be
 * verified without any production app or personal data.
 *
 * 另提供受控入口：同一任务的第二页、独立第二任务、:remote 进程页面，以及可显式
 * 起停的 MediaSession/前台服务。这些能力只服务于受控验证，不读取或操作用户应用。
 */
public final class ProbeActivity extends Activity {
    private int taps;
    private TextView status;

    @Override protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(60, 160, 60, 60);

        TextView title = new TextView(this);
        title.setText("mobile-use probe");
        title.setTextSize(28);

        EditText input = new EditText(this);
        input.setHint("输入文字…");
        input.setInputType(InputType.TYPE_CLASS_TEXT);
        input.setTextSize(22);
        input.setSingleLine(true);

        Button button = new Button(this);
        button.setText("TAP");
        button.setTextSize(22);
        TextView counter = new TextView(this);
        counter.setText("taps: 0");

        status = new TextView(this);
        status.setText("状态：就绪");
        status.setTextSize(20);

        button.setOnClickListener(view -> {
            taps++;
            counter.setText("taps: " + taps);
            status.setText("状态：已点击 " + taps + " 次");
        });

        CheckBox check = new CheckBox(this);
        check.setText("CHECK");

        Button secondPage = action("打开第二页", () -> {
            startActivity(new Intent(this, ProbeSecondActivity.class));
            status.setText("状态：已请求打开第二页（同一任务）");
        });
        Button secondTask = action("新建第二任务", () -> {
            Intent intent = new Intent(this, ProbeSecondActivity.class);
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            startActivity(intent);
            status.setText("状态：已请求第二任务");
        });
        Button remotePage = action("打开远程进程页面", () -> {
            Intent intent = new Intent(this, ProbeRemoteActivity.class);
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            startActivity(intent);
            status.setText("状态：已请求远程进程页面");
        });
        Button startPlayback = action("开始后台播放", () -> {
            Intent intent = new Intent(this, ProbePlaybackService.class)
                    .setAction(ProbePlaybackService.ACTION_START);
            startForegroundService(intent);
            status.setText("状态：已请求后台播放（前台服务）");
        });
        Button stopPlayback = action("停止后台播放", () -> {
            Intent intent = new Intent(this, ProbePlaybackService.class)
                    .setAction(ProbePlaybackService.ACTION_STOP);
            startService(intent);
            status.setText("状态：已请求停止后台播放");
        });

        TextView note = new TextView(this);
        note.setText("受控测试目标：页面栈 / 第二任务 / 远程进程 / 前台播放");
        note.setTextSize(16);

        append(root, title);
        append(root, input);
        append(root, button);
        append(root, counter);
        append(root, check);
        append(root, status);
        append(root, secondPage);
        append(root, secondTask);
        append(root, remotePage);
        append(root, startPlayback);
        append(root, stopPlayback);
        append(root, note);
        setContentView(root);
        getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
    }

    private static void append(LinearLayout root, View view) {
        root.addView(view, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
    }

    private Button action(String text, Runnable onClick) {
        Button button = new Button(this);
        button.setText(text);
        button.setTextSize(18);
        button.setOnClickListener(view -> onClick.run());
        return button;
    }
}
