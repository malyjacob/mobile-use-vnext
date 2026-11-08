package com.mobileuse.probe;

import android.app.Activity;
import android.os.Bundle;
import android.text.InputType;
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
 */
public final class ProbeActivity extends Activity {
    private int taps;

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
        button.setOnClickListener(view -> {
            taps++;
            counter.setText("taps: " + taps);
        });

        CheckBox check = new CheckBox(this);
        check.setText("CHECK");

        root.addView(title, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        root.addView(input, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        root.addView(button, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        root.addView(counter, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        root.addView(check, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        setContentView(root);
        getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
    }
}
