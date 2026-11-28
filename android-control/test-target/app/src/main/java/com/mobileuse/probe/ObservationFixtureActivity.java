package com.mobileuse.probe;

import android.app.Activity;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.graphics.Color;
import android.graphics.drawable.ColorDrawable;
import android.os.Bundle;
import android.text.Editable;
import android.text.InputType;
import android.text.TextWatcher;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.WindowManager;
import android.view.inputmethod.InputMethodManager;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.GridLayout;
import android.widget.HorizontalScrollView;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.Space;
import android.widget.TextView;

import java.util.Locale;

public final class ObservationFixtureActivity extends Activity {
    public static final String ACTION_UPDATE =
            "com.mobileuse.probe.action.OBSERVATION_FIXTURE_UPDATE";

    private static final String EXTRA_SCENE = "scene";
    private static final String EXTRA_REVISION = "revision";
    private static final String EXTRA_D01_EMPTY_COUNT = "d01EmptyCount";
    private static final String EXTRA_D02_WINDOW = "d02Window";
    private static final String EXTRA_D02_BODY = "d02Body";
    private static final String EXTRA_D02_WRAPPER = "d02Wrapper";
    private static final String EXTRA_D03_VARIANT = "d03Variant";
    private static final String EXTRA_D03_SCROLL = "d03Scroll";
    private static final String EXTRA_D03_HINT = "d03Hint";
    private static final String EXTRA_D04_FOCUS = "d04Focus";
    private static final String EXTRA_D04_KEYBOARD = "d04Keyboard";
    private static final String EXTRA_D04_SOURCE = "d04Source";

    private static final String SCENE_D01 = "d01";
    private static final String SCENE_D02 = "d02";
    private static final String SCENE_D03 = "d03";
    private static final String SCENE_D04 = "d04";
    private static final String SCENE_D05 = "d05";
    private static final String SCENE_D06 = "d06";
    private static final String SCENE_D07 = "d07";
    private static final String SCENE_D08 = "d08";
    private static final String SCENE_D09 = "d09";
    private static final String SCENE_D10 = "d10";

    private static final int COLOR_BACKGROUND = Color.rgb(248, 249, 251);
    private static final int COLOR_SURFACE = Color.rgb(255, 255, 255);
    private static final int COLOR_TEXT = Color.rgb(24, 32, 40);
    private static final int COLOR_MUTED = Color.rgb(72, 84, 96);
    private static final int COLOR_ACCENT = Color.rgb(22, 112, 164);

    private static final int[] D02_CARD_IDS = {
            R.id.observation_d02_card_a,
            R.id.observation_d02_card_b,
            R.id.observation_d02_card_c,
            R.id.observation_d02_card_d,
            R.id.observation_d02_card_e,
            R.id.observation_d02_card_f
    };

    private static final int[] D02_BODY_IDS = {
            R.id.observation_d02_body_a,
            R.id.observation_d02_body_b,
            R.id.observation_d02_body_c,
            R.id.observation_d02_body_d,
            R.id.observation_d02_body_e,
            R.id.observation_d02_body_f
    };

    private boolean receiverRegistered;
    private BroadcastReceiver commandReceiver;

    private String scene = SCENE_D01;
    private int revision;
    private int buildNumber;
    private int d01EmptyCount = 32;
    private String d02Window = "a-e";
    private String d02Body = "stable";
    private boolean d02Wrapper;
    private String d03Variant = "carrier-a";
    private String d03Scroll = "normal";
    private String d03Hint = "expanded";
    private String d04Focus = "none";
    private String d04Keyboard = "hidden";
    private String d04Source = "stable";
    private boolean d04ImeVisible;

    private int d01SemanticTaps;
    private int d03ItemTaps;
    private int d03BlockerTaps;
    private int d04TextEditsA;
    private int d04TextEditsB;

    private TextView currentD01State;
    private TextView currentD02State;
    private TextView currentD03State;
    private TextView currentD04State;
    private EditText currentD04InputA;
    private EditText currentD04InputB;
    private TextView currentD08State;
    private TextView currentD09State;
    private TextView currentD10State;
    private int d08CardTaps;
    private int d08BlockerTaps;
    private int d09CardTaps;
    private int d10ParentTaps;
    private int d10ChildTaps;
    private int d10ChildLongTaps;
    private int d10BadgeTaps;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        getWindow().setSoftInputMode(
                WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE
                        | WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_HIDDEN);

        commandReceiver = new BroadcastReceiver() {
            @Override
            public void onReceive(Context context, Intent intent) {
                if (acceptsCommand(intent)) {
                    applyCommand(intent, false);
                }
            }
        };
        IntentFilter filter = new IntentFilter(ACTION_UPDATE);
        registerReceiver(commandReceiver, filter, Context.RECEIVER_EXPORTED);
        receiverRegistered = true;
        applyCommand(getIntent(), true);
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        applyCommand(intent, false);
    }

    @Override
    protected void onDestroy() {
        if (receiverRegistered) {
            unregisterReceiver(commandReceiver);
            receiverRegistered = false;
        }
        super.onDestroy();
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        updateD04State();
    }

    private boolean acceptsCommand(Intent intent) {
        return intent != null
                && ACTION_UPDATE.equals(intent.getAction())
                && getPackageName().equals(intent.getPackage());
    }

    private void applyCommand(Intent intent, boolean initial) {
        boolean rebuild = initial;
        boolean focusCommand = false;
        boolean keyboardCommand = false;

        String requestedScene = intent.getStringExtra(EXTRA_SCENE);
        if (isFixtureScene(requestedScene) && !requestedScene.equals(scene)) {
            scene = requestedScene;
        }
        if (isFixtureScene(requestedScene)) {
            rebuild = true;
        }

        if (intent.hasExtra(EXTRA_REVISION)) {
            int requestedRevision = Math.max(0, intent.getIntExtra(EXTRA_REVISION, revision));
            revision = requestedRevision;
            rebuild = true;
        }

        int requestedEmptyCount = intent.getIntExtra(EXTRA_D01_EMPTY_COUNT, d01EmptyCount);
        if ((requestedEmptyCount == 0 || requestedEmptyCount == 32 || requestedEmptyCount == 256)
                && requestedEmptyCount != d01EmptyCount) {
            d01EmptyCount = requestedEmptyCount;
        }
        if (intent.hasExtra(EXTRA_D01_EMPTY_COUNT)
                && (requestedEmptyCount == 0 || requestedEmptyCount == 32 || requestedEmptyCount == 256)) {
            rebuild = true;
        }

        String requestedD02Window = intent.getStringExtra(EXTRA_D02_WINDOW);
        if (isOneOf(requestedD02Window, "a-e", "b-f")
                && !requestedD02Window.equals(d02Window)) {
            d02Window = requestedD02Window;
        }
        if (isOneOf(requestedD02Window, "a-e", "b-f")) {
            rebuild = true;
        }

        String requestedD02Body = intent.getStringExtra(EXTRA_D02_BODY);
        if (isOneOf(requestedD02Body, "stable", "changed")
                && !requestedD02Body.equals(d02Body)) {
            d02Body = requestedD02Body;
        }
        if (isOneOf(requestedD02Body, "stable", "changed")) {
            rebuild = true;
        }

        if (intent.hasExtra(EXTRA_D02_WRAPPER)) {
            boolean requestedWrapper = intent.getIntExtra(EXTRA_D02_WRAPPER, d02Wrapper ? 1 : 0) != 0;
            if (requestedWrapper != d02Wrapper) {
                d02Wrapper = requestedWrapper;
            }
            rebuild = true;
        }

        String requestedD03Variant = intent.getStringExtra(EXTRA_D03_VARIANT);
        if (isOneOf(requestedD03Variant,
                "carrier-a", "carrier-b", "carrier-moved", "child-blocker",
                "full-overlay", "carrier-none")
                && !requestedD03Variant.equals(d03Variant)) {
            d03Variant = requestedD03Variant;
        }
        if (isOneOf(requestedD03Variant,
                "carrier-a", "carrier-b", "carrier-moved", "child-blocker",
                "full-overlay", "carrier-none")) {
            rebuild = true;
        }

        String requestedD03Scroll = intent.getStringExtra(EXTRA_D03_SCROLL);
        if (isOneOf(requestedD03Scroll,
                "normal", "stretch", "offaxis", "blocker", "full-blocker",
                "short", "ambiguous")
                && !requestedD03Scroll.equals(d03Scroll)) {
            d03Scroll = requestedD03Scroll;
        }
        if (isOneOf(requestedD03Scroll,
                "normal", "stretch", "offaxis", "blocker", "full-blocker",
                "short", "ambiguous")) {
            rebuild = true;
        }

        String requestedD03Hint = intent.getStringExtra(EXTRA_D03_HINT);
        if (isOneOf(requestedD03Hint, "expanded", "collapsed")
                && !requestedD03Hint.equals(d03Hint)) {
            d03Hint = requestedD03Hint;
        }
        if (isOneOf(requestedD03Hint, "expanded", "collapsed")) {
            rebuild = true;
        }

        String requestedD04Focus = intent.getStringExtra(EXTRA_D04_FOCUS);
        if (isOneOf(requestedD04Focus, "none", "a", "b")) {
            d04Focus = requestedD04Focus;
            focusCommand = true;
        }

        String requestedD04Keyboard = intent.getStringExtra(EXTRA_D04_KEYBOARD);
        if (isOneOf(requestedD04Keyboard, "hidden", "shown")) {
            d04Keyboard = requestedD04Keyboard;
            keyboardCommand = true;
        }

        String requestedD04Source = intent.getStringExtra(EXTRA_D04_SOURCE);
        if (isOneOf(requestedD04Source,
                "stable", "alternate-description", "missing-description",
                "same-description", "duplicate-anchor")
                && !requestedD04Source.equals(d04Source)) {
            d04Source = requestedD04Source;
        }
        if (isOneOf(requestedD04Source,
                "stable", "alternate-description", "missing-description",
                "same-description", "duplicate-anchor")) {
            rebuild = true;
        }

        if (rebuild) {
            resetActionCounters();
            buildNumber++;
            render();
        }

        if (SCENE_D04.equals(scene) && (focusCommand || keyboardCommand)) {
            applyD04FocusAndKeyboard(focusCommand, keyboardCommand);
        }
    }

    private void resetActionCounters() {
        d01SemanticTaps = 0;
        d03ItemTaps = 0;
        d03BlockerTaps = 0;
        d04TextEditsA = 0;
        d04TextEditsB = 0;
    }

    private void render() {
        currentD01State = null;
        currentD02State = null;
        currentD03State = null;
        currentD04State = null;
        currentD04InputA = null;
        currentD04InputB = null;
        currentD08State = null;
        currentD09State = null;
        currentD10State = null;
        d08CardTaps = 0;
        d08BlockerTaps = 0;
        d09CardTaps = 0;
        d10ParentTaps = 0;
        d10ChildTaps = 0;
        d10ChildLongTaps = 0;
        d10BadgeTaps = 0;

        if (SCENE_D02.equals(scene)) {
            renderD02();
        } else if (SCENE_D03.equals(scene)) {
            renderD03();
        } else if (SCENE_D04.equals(scene)) {
            renderD04();
        } else if (SCENE_D05.equals(scene)) {
            renderD05();
        } else if (SCENE_D06.equals(scene)) {
            renderD06();
        } else if (SCENE_D07.equals(scene)) {
            renderD07();
        } else if (SCENE_D08.equals(scene)) {
            renderD08();
        } else if (SCENE_D09.equals(scene)) {
            renderD09();
        } else if (SCENE_D10.equals(scene)) {
            renderD10();
        } else {
            renderD01();
        }
    }

    private boolean isFixtureScene(String name) {
        return isOneOf(name, SCENE_D01, SCENE_D02, SCENE_D03, SCENE_D04,
                SCENE_D05, SCENE_D06, SCENE_D07, SCENE_D08, SCENE_D09, SCENE_D10);
    }

    private void renderD01() {
        LinearLayout root = baseRoot(R.id.observation_d01_root);
        root.addView(heading(R.id.observation_d01_heading, "D01 purification fixture"));

        currentD01State = stateText(R.id.observation_d01_state);
        root.addView(currentD01State, verticalParams(-2, 0));

        GridLayout grid = new GridLayout(this);
        grid.setId(R.id.observation_d01_empty_region);
        int columns = d01EmptyCount == 32 ? 8 : 16;
        int cell = d01EmptyCount == 32 ? 12 : 6;
        grid.setColumnCount(columns);
        for (int index = 0; index < d01EmptyCount; index++) {
            View empty = new View(this);
            empty.setId(R.id.observation_d01_empty_leaf);
            empty.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_YES);
            GridLayout.LayoutParams params = new GridLayout.LayoutParams(
                    GridLayout.spec(index / columns),
                    GridLayout.spec(index % columns));
            params.width = dp(cell);
            params.height = dp(cell);
            grid.addView(empty, params);
        }
        root.addView(grid, verticalParams(-2, 12));

        TextView semanticText = new TextView(this);
        semanticText.setId(R.id.observation_d01_semantic_text);
        semanticText.setText("D01 semantic sibling: readable text");
        semanticText.setTextSize(18);
        semanticText.setTextColor(COLOR_TEXT);
        semanticText.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_YES);
        semanticText.setPadding(0, dp(8), 0, dp(8));
        root.addView(semanticText, verticalParams(-2, 8));

        Button semanticAction = new Button(this);
        semanticAction.setId(R.id.observation_d01_semantic_action);
        semanticAction.setText("D01 semantic action");
        semanticAction.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_YES);
        semanticAction.setOnClickListener(view -> {
            d01SemanticTaps++;
            updateD01State();
        });
        root.addView(semanticAction, verticalParams(-2, 8));

        TextView note = plainText(
                "Empty Views keep package-local IDs but no label, description, action or state.");
        root.addView(note, verticalParams(-2, 12));

        setContentView(root);
        updateD01State();
    }

    private void renderD02() {
        LinearLayout root = baseRoot(R.id.observation_d02_root);
        root.addView(heading(R.id.observation_d02_heading, "D02 continuity and delta fixture"));

        currentD02State = stateText(R.id.observation_d02_state);
        root.addView(currentD02State, verticalParams(-2, 0));

        ScrollView scroll = new ScrollView(this);
        scroll.setId(R.id.observation_d02_scroll);
        scroll.setContentDescription("D02 five-card stable scroll");
        scroll.setFillViewport(false);

        LinearLayout list = new LinearLayout(this);
        list.setId(R.id.observation_d02_list);
        list.setOrientation(LinearLayout.VERTICAL);
        String letters = "b-f".equals(d02Window) ? "BCDEF" : "ABCDE";
        for (int index = 0; index < letters.length(); index++) {
            char letter = letters.charAt(index);
            int slot = letter - 'A';
            LinearLayout card = new LinearLayout(this);
            card.setId(D02_CARD_IDS[slot]);
            card.setOrientation(LinearLayout.VERTICAL);
            card.setPadding(dp(14), dp(8), dp(14), dp(8));
            card.setBackgroundColor(index % 2 == 0 ? COLOR_SURFACE : Color.rgb(239, 245, 249));

            TextView title = new TextView(this);
            title.setId(R.id.observation_d02_title);
            title.setText(String.format(Locale.ROOT, "D02 unique card title %c", letter));
            title.setTextSize(18);
            title.setTextColor(COLOR_TEXT);
            card.addView(title, verticalParams(-2, 0));

            TextView body = new TextView(this);
            body.setId(D02_BODY_IDS[slot]);
            String bodyText = String.format(Locale.ROOT, "D02 body %c", letter);
            if ("changed".equals(d02Body) && letter == 'B') {
                bodyText = String.format(Locale.ROOT, "D02 body B changed at revision %d", revision);
            }
            body.setText(bodyText);
            body.setTextSize(15);
            body.setTextColor(COLOR_MUTED);
            card.addView(body, verticalParams(-2, 2));

            TextView meta = new TextView(this);
            meta.setId(R.id.observation_d02_meta);
            meta.setText("synthetic card metadata");
            meta.setTextSize(13);
            meta.setTextColor(COLOR_MUTED);
            card.addView(meta, verticalParams(-2, 2));

            View cardView = card;
            if (d02Wrapper && letter == 'B') {
                FrameLayout wrapper = new FrameLayout(this);
                wrapper.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_YES);
                wrapper.addView(card, new FrameLayout.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT,
                        ViewGroup.LayoutParams.WRAP_CONTENT));
                cardView = wrapper;
            }
            list.addView(cardView, verticalParams(-2, 6));
        }
        scroll.addView(list, new ScrollView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));
        root.addView(scroll, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f));

        TextView footer = plainText("D02 stable footer: five cards, shared title ID, unique titles.");
        footer.setId(R.id.observation_d02_footer);
        root.addView(footer, verticalParams(-2, 8));

        setContentView(root);
        updateD02State();
    }

    private void renderD03() {
        LinearLayout root = baseRoot(R.id.observation_d03_root);
        root.addView(heading(R.id.observation_d03_heading, "D03 action revalidation fixture"));

        currentD03State = stateText(R.id.observation_d03_state);
        root.addView(currentD03State, verticalParams(-2, 0));

        TextView hint = new TextView(this);
        hint.setId(R.id.observation_d03_hint);
        hint.setText("D03 synthetic action hint");
        hint.setTextSize(15);
        hint.setTextColor(COLOR_TEXT);
        hint.setPadding(dp(10), dp(6), dp(10), dp(6));
        hint.setBackgroundColor(Color.rgb(255, 244, 205));
        hint.setVisibility("collapsed".equals(d03Hint) ? View.GONE : View.VISIBLE);
        root.addView(hint, verticalParams(-2, 8));

        root.addView(buildD03Item(), verticalParams(dp(132), 8));
        root.addView(buildD03Scroll(), verticalParams(d03ScrollHeight(), 8));

        TextView note = plainText(
                "Use the stable TextView above as the state proof; overlay taps are separate.");
        root.addView(note, verticalParams(-2, 8));

        setContentView(root);
        updateD03State();
    }

    private FrameLayout buildD03Item() {
        FrameLayout item = new FrameLayout(this);
        item.setId(R.id.observation_d03_item);
        item.setContentDescription("D03 item_42 independent stable anchor");
        item.setClickable(true);
        item.setFocusable(true);
        item.setBackgroundColor(Color.rgb(225, 240, 247));
        item.setOnClickListener(view -> {
            d03ItemTaps++;
            updateD03State();
        });

        TextView label = new TextView(this);
        label.setId(R.id.observation_d03_item_label);
        label.setText("item_42");
        label.setTextSize(18);
        label.setTextColor(COLOR_TEXT);
        label.setGravity(Gravity.TOP | Gravity.CENTER_HORIZONTAL);
        FrameLayout.LayoutParams labelParams = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
                Gravity.TOP | Gravity.CENTER_HORIZONTAL);
        labelParams.topMargin = dp(10);
        item.addView(label, labelParams);

        if (!"carrier-none".equals(d03Variant)) {
            ImageView carrier = new ImageView(this);
            boolean carrierB = !"carrier-a".equals(d03Variant);
            carrier.setId(carrierB
                    ? R.id.observation_d03_carrier_b
                    : R.id.observation_d03_carrier_a);
            carrier.setImageDrawable(new ColorDrawable(
                    carrierB ? Color.rgb(34, 139, 134) : Color.rgb(53, 111, 179)));
            carrier.setScaleType(ImageView.ScaleType.CENTER_INSIDE);
            carrier.setClickable(false);
            FrameLayout.LayoutParams carrierParams = new FrameLayout.LayoutParams(
                    dp(64), dp(48), Gravity.CENTER);
            if ("carrier-moved".equals(d03Variant)) {
                carrier.setTranslationY(dp(12));
            }
            item.addView(carrier, carrierParams);
        }

        if ("child-blocker".equals(d03Variant)) {
            Button blocker = new Button(this);
            blocker.setId(R.id.observation_d03_child_blocker);
            blocker.setText("child blocker");
            blocker.setTextSize(14);
            blocker.setOnClickListener(view -> {
                d03BlockerTaps++;
                updateD03State();
            });
            FrameLayout.LayoutParams blockerParams = new FrameLayout.LayoutParams(
                    dp(144), dp(44), Gravity.BOTTOM | Gravity.CENTER_HORIZONTAL);
            blockerParams.bottomMargin = dp(8);
            item.addView(blocker, blockerParams);
        }

        if ("full-overlay".equals(d03Variant)) {
            View overlay = new View(this);
            overlay.setId(R.id.observation_d03_overlay);
            overlay.setBackgroundColor(Color.argb(90, 184, 54, 54));
            overlay.setClickable(true);
            overlay.setOnClickListener(view -> {
                d03BlockerTaps++;
                updateD03State();
            });
            item.addView(overlay, new FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT));
        }

        item.setMinimumHeight(dp(132));
        return item;
    }

    private View buildD03Scroll() {
        if ("ambiguous".equals(d03Scroll)) {
            LinearLayout stack = new LinearLayout(this);
            stack.setId(R.id.observation_d03_scroll_stack);
            stack.setOrientation(LinearLayout.VERTICAL);
            stack.addView(createD03ScrollFrame(82, 0), verticalParams(dp(82), 0));
            stack.addView(createD03ScrollFrame(82, 1), verticalParams(dp(82), 6));
            return stack;
        }

        int height = 180;
        if ("stretch".equals(d03Scroll)) {
            height = 260;
        } else if ("short".equals(d03Scroll)) {
            height = 56;
        }
        return createD03ScrollFrame(height, 0);
    }

    private int d03ScrollHeight() {
        if ("ambiguous".equals(d03Scroll)) {
            return dp(170);
        }
        if ("stretch".equals(d03Scroll)) {
            return dp(260);
        }
        if ("short".equals(d03Scroll)) {
            return dp(56);
        }
        return dp(180);
    }

    private FrameLayout createD03ScrollFrame(int heightDp, int duplicateIndex) {
        FrameLayout frame = new FrameLayout(this);
        frame.setId(R.id.observation_d03_scroll_frame);

        ScrollView scroll = new ScrollView(this);
        scroll.setId(R.id.observation_d03_scroll);
        scroll.setContentDescription("D03 stable scroll container");
        scroll.setFillViewport(true);
        if ("offaxis".equals(d03Scroll)) {
            scroll.setTranslationX(dp(48));
        }

        LinearLayout content = new LinearLayout(this);
        content.setId(R.id.observation_d03_scroll_content);
        content.setOrientation(LinearLayout.VERTICAL);
        for (int row = 1; row <= 12; row++) {
            TextView rowView = new TextView(this);
            rowView.setId(R.id.observation_d03_scroll_row);
            rowView.setText(String.format(
                    Locale.ROOT,
                    "D03 scroll row %02d %s",
                    row,
                    duplicateIndex == 0 ? "A" : "B"));
            rowView.setTextSize(14);
            rowView.setTextColor(COLOR_TEXT);
            rowView.setPadding(dp(8), dp(6), dp(8), dp(6));
            content.addView(rowView, verticalParams(-2, 0));
        }
        scroll.addView(content, new ScrollView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));

        frame.addView(scroll, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT));

        if ("blocker".equals(d03Scroll)) {
            View blocker = new View(this);
            blocker.setId(R.id.observation_d03_scroll_blocker);
            blocker.setBackgroundColor(Color.argb(95, 184, 54, 54));
            blocker.setClickable(true);
            blocker.setOnClickListener(view -> {
                d03BlockerTaps++;
                updateD03State();
            });
            FrameLayout.LayoutParams blockerParams = new FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, dp(64), Gravity.CENTER);
            frame.addView(blocker, blockerParams);
        } else if ("full-blocker".equals(d03Scroll)) {
            View blocker = new View(this);
            blocker.setId(R.id.observation_d03_scroll_blocker);
            blocker.setBackgroundColor(Color.argb(95, 184, 54, 54));
            blocker.setClickable(true);
            blocker.setOnClickListener(view -> {
                d03BlockerTaps++;
                updateD03State();
            });
            frame.addView(blocker, new FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT));
        }

        frame.setMinimumHeight(dp(heightDp));
        frame.setLayoutParams(new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(heightDp)));
        return frame;
    }

    private void renderD04() {
        LinearLayout root = baseRoot(R.id.observation_d04_root);
        root.addView(heading(R.id.observation_d04_heading, "D04 text focus and source fixture"));

        currentD04State = stateText(R.id.observation_d04_state);
        root.addView(currentD04State, verticalParams(-2, 0));

        for (int index = 1; index <= 4; index++) {
            TextView stableRow = new TextView(this);
            stableRow.setId(R.id.observation_d04_stable_row);
            stableRow.setText(String.format(
                    Locale.ROOT, "D04 stable content row %02d", index));
            stableRow.setTextSize(14);
            stableRow.setTextColor(COLOR_MUTED);
            root.addView(stableRow, verticalParams(-2, 2));
        }

        Space spacer = new Space(this);
        root.addView(spacer, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f));

        TextView labelA = fieldLabel(R.id.observation_d04_label_a, "Input A anchor");
        root.addView(labelA, verticalParams(-2, 8));

        currentD04InputA = inputField(
                R.id.observation_d04_input_a,
                "D04 input anchor A",
                "Synthetic input A",
                true);
        root.addView(currentD04InputA, verticalParams(-2, 4));

        TextView labelB = fieldLabel(R.id.observation_d04_label_b, "Input B anchor");
        root.addView(labelB, verticalParams(-2, 8));

        int inputBId = "duplicate-anchor".equals(d04Source)
                ? R.id.observation_d04_input_a
                : R.id.observation_d04_input_b;
        currentD04InputB = inputField(
                inputBId,
                inputBDescription(),
                "Synthetic input B",
                false);
        root.addView(currentD04InputB, verticalParams(-2, 4));

        TextView footer = plainText(
                "Focus broadcasts use the current tree; source changes rebuild the form.");
        footer.setId(R.id.observation_d04_footer);
        root.addView(footer, verticalParams(-2, 8));

        int softInputState = "shown".equals(d04Keyboard)
                ? WindowManager.LayoutParams.SOFT_INPUT_STATE_UNSPECIFIED
                : WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_HIDDEN;
        getWindow().setSoftInputMode(
                WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE | softInputState);

        setContentView(root);
        updateD04State();
    }

    private void renderD05() {
        LinearLayout root = baseRoot(View.generateViewId());
        root.addView(heading(View.generateViewId(), "D05 horizontal pager over vertical list"));

        HorizontalScrollView pager = new HorizontalScrollView(this);
        pager.setId(View.generateViewId());
        pager.setContentDescription("D05 outer horizontal pager");
        pager.setFillViewport(true);
        pager.setBackgroundColor(Color.rgb(235, 242, 248));

        LinearLayout pages = new LinearLayout(this);
        pages.setId(View.generateViewId());
        pages.setOrientation(LinearLayout.HORIZONTAL);

        ScrollView inner = new ScrollView(this);
        inner.setId(View.generateViewId());
        inner.setContentDescription("D05 inner vertical list");
        inner.setFillViewport(false);

        LinearLayout rows = new LinearLayout(this);
        rows.setId(View.generateViewId());
        rows.setOrientation(LinearLayout.VERTICAL);
        for (int index = 1; index <= 40; index++) {
            TextView row = plainText(String.format(Locale.ROOT, "D05 pager row %02d", index));
            row.setTextSize(16);
            row.setTextColor(COLOR_TEXT);
            rows.addView(row, verticalParams(-2, 4));
        }
        inner.addView(rows, new ScrollView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));
        pages.addView(inner, new LinearLayout.LayoutParams(
                dp(400), ViewGroup.LayoutParams.MATCH_PARENT));

        TextView secondPage = plainText("D05 second pager page");
        secondPage.setId(View.generateViewId());
        secondPage.setTextSize(16);
        secondPage.setGravity(Gravity.CENTER);
        pages.addView(secondPage, new LinearLayout.LayoutParams(
                dp(400), ViewGroup.LayoutParams.MATCH_PARENT));

        pager.addView(pages, new HorizontalScrollView.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.MATCH_PARENT));
        root.addView(pager, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(420)));

        root.addView(plainText("D05 end: scroll down on the pager must drive the inner list."),
                verticalParams(-2, 8));
        setContentView(root);
    }

    private void renderD06() {
        LinearLayout root = baseRoot(View.generateViewId());
        root.addView(heading(View.generateViewId(), "D06 non-scroll wrapper over vertical list"));

        FrameLayout wrapper = new FrameLayout(this);
        wrapper.setId(View.generateViewId());
        wrapper.setContentDescription("D06 non-scroll wrapper");
        wrapper.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_YES);

        ScrollView inner = new ScrollView(this);
        inner.setId(View.generateViewId());
        inner.setContentDescription("D06 wrapper inner vertical list");

        LinearLayout rows = new LinearLayout(this);
        rows.setId(View.generateViewId());
        rows.setOrientation(LinearLayout.VERTICAL);
        for (int index = 1; index <= 14; index++) {
            TextView row = plainText(String.format(Locale.ROOT, "D06 wrapper row %02d", index));
            row.setTextSize(16);
            row.setTextColor(COLOR_TEXT);
            rows.addView(row, verticalParams(-2, 6));
        }
        inner.addView(rows, new ScrollView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));
        wrapper.addView(inner, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT));
        root.addView(wrapper, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(360)));

        root.addView(plainText("D06 end: scroll down on the wrapper must drive the inner list."),
                verticalParams(-2, 8));
        setContentView(root);
    }

    private void renderD07() {
        LinearLayout root = baseRoot(View.generateViewId());
        root.addView(heading(View.generateViewId(), "D07 wrapped and flat text targets"));

        FrameLayout wrapper = new FrameLayout(this);
        wrapper.setId(View.generateViewId());
        wrapper.setContentDescription("D07 wrapped input field");
        wrapper.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_YES);
        wrapper.setPadding(dp(12), dp(8), dp(12), dp(8));
        wrapper.setBackgroundColor(Color.rgb(232, 240, 247));

        EditText wrapped = new EditText(this);
        wrapped.setId(View.generateViewId());
        wrapped.setContentDescription("D07 wrapped editable");
        wrapped.setHint("D07 wrapped input");
        wrapped.setInputType(InputType.TYPE_CLASS_TEXT);
        wrapped.setSingleLine(true);
        wrapper.addView(wrapped, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT));
        root.addView(wrapper, verticalParams(dp(72), 12));

        FrameLayout flat = new FrameLayout(this);
        flat.setId(View.generateViewId());
        flat.setContentDescription("D07 flat field");
        flat.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_YES);

        EditText inner = new EditText(this);
        inner.setId(View.generateViewId());
        inner.setContentDescription("D07 flat editable");
        inner.setHint("D07 flat input");
        inner.setInputType(InputType.TYPE_CLASS_TEXT);
        inner.setSingleLine(true);
        flat.addView(inner, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(64)));

        TextView placeholder = new TextView(this);
        placeholder.setId(View.generateViewId());
        placeholder.setText("D07 placeholder label");
        placeholder.setTextSize(16);
        placeholder.setTextColor(COLOR_MUTED);
        placeholder.setGravity(Gravity.CENTER);
        flat.addView(placeholder, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
                Gravity.CENTER));
        root.addView(flat, verticalParams(dp(72), 12));

        root.addView(plainText("D07 end: text(ref) may resolve a wrapper or a co-located label."),
                verticalParams(-2, 8));
        setContentView(root);
    }

    private void renderD08() {
        LinearLayout root = baseRoot(View.generateViewId());
        root.addView(heading(View.generateViewId(), "D08 residual safety patch card"));

        currentD08State = stateText(View.generateViewId());
        root.addView(currentD08State, verticalParams(-2, 0));

        FrameLayout card = new FrameLayout(this);
        card.setId(View.generateViewId());
        card.setContentDescription("D08 residual patch card");
        card.setClickable(true);
        card.setFocusable(true);
        card.setBackgroundColor(Color.rgb(225, 240, 247));
        card.setOnClickListener(view -> {
            d08CardTaps++;
            updateD08State();
        });

        Button blocker = new Button(this);
        blocker.setId(View.generateViewId());
        blocker.setText("D08 center blocker");
        blocker.setTextSize(14);
        blocker.setOnClickListener(view -> {
            d08BlockerTaps++;
            updateD08State();
        });
        FrameLayout.LayoutParams blockerParams = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
                Gravity.CENTER);
        blockerParams.setMargins(dp(34), dp(34), dp(34), dp(34));
        card.addView(blocker, blockerParams);

        root.addView(card, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(320)));
        root.addView(plainText("D08 end: tap(card) must land on the free border, not the blocker."),
                verticalParams(-2, 8));
        setContentView(root);
        updateD08State();
    }

    private void renderD09() {
        LinearLayout root = baseRoot(View.generateViewId());
        root.addView(heading(View.generateViewId(), "D09 passive overlap card"));

        currentD09State = stateText(View.generateViewId());
        root.addView(currentD09State, verticalParams(-2, 0));

        FrameLayout card = new FrameLayout(this);
        card.setId(View.generateViewId());
        card.setContentDescription("D09 passive overlap card");
        card.setClickable(true);
        card.setFocusable(true);
        card.setBackgroundColor(Color.rgb(225, 240, 247));
        card.setOnClickListener(view -> {
            d09CardTaps++;
            updateD09State();
        });

        ImageView cover = new ImageView(this);
        cover.setId(View.generateViewId());
        cover.setImageDrawable(new ColorDrawable(Color.rgb(53, 111, 179)));
        cover.setScaleType(ImageView.ScaleType.FIT_XY);
        cover.setClickable(false);
        card.addView(cover, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT));

        TextView title = new TextView(this);
        title.setId(View.generateViewId());
        title.setText("D09 passive title");
        title.setTextSize(20);
        title.setTextColor(Color.WHITE);
        title.setGravity(Gravity.CENTER);
        title.setClickable(false);
        card.addView(title, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
                Gravity.CENTER));

        root.addView(card, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(220)));
        root.addView(plainText("D09 end: tap(card) must pass through passive image and title."),
                verticalParams(-2, 8));
        setContentView(root);
        updateD09State();
    }

    private void renderD10() {
        LinearLayout root = baseRoot(View.generateViewId());
        root.addView(heading(View.generateViewId(), "D10 coextensive child and badge"));

        currentD10State = stateText(View.generateViewId());
        root.addView(currentD10State, verticalParams(-2, 0));

        FrameLayout card = new FrameLayout(this);
        card.setId(View.generateViewId());
        card.setContentDescription("D10 coextensive card");
        card.setClickable(true);
        card.setFocusable(true);
        card.setBackgroundColor(Color.rgb(225, 240, 247));
        card.setOnClickListener(view -> {
            d10ParentTaps++;
            updateD10State();
        });

        FrameLayout child = new FrameLayout(this);
        child.setId(View.generateViewId());
        child.setContentDescription("D10 coextensive child");
        child.setClickable(true);
        child.setFocusable(true);
        child.setLongClickable(true);
        child.setBackgroundColor(Color.rgb(53, 111, 179));
        child.setOnClickListener(view -> {
            d10ChildTaps++;
            updateD10State();
        });
        child.setOnLongClickListener(view -> {
            d10ChildLongTaps++;
            updateD10State();
            return true;
        });
        card.addView(child, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT));

        Button badge = new Button(this);
        badge.setId(View.generateViewId());
        badge.setText("x");
        badge.setTextSize(14);
        badge.setOnClickListener(view -> {
            d10BadgeTaps++;
            updateD10State();
        });
        FrameLayout.LayoutParams badgeParams = new FrameLayout.LayoutParams(
                dp(44), dp(24), Gravity.TOP | Gravity.END);
        badgeParams.topMargin = dp(4);
        badgeParams.rightMargin = dp(4);
        card.addView(badge, badgeParams);

        root.addView(card, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(180)));
        root.addView(plainText(
                "D10 end: tap(card) delegates to the child; long_press(card) lifts to the child."),
                verticalParams(-2, 8));
        setContentView(root);
        updateD10State();
    }

    private void updateD08State() {
        if (currentD08State == null) {
            return;
        }
        currentD08State.setText(String.format(
                Locale.ROOT,
                "scene=d08 revision=%d build=%d cardTaps=%d blockerTaps=%d",
                revision,
                buildNumber,
                d08CardTaps,
                d08BlockerTaps));
    }

    private void updateD09State() {
        if (currentD09State == null) {
            return;
        }
        currentD09State.setText(String.format(
                Locale.ROOT,
                "scene=d09 revision=%d build=%d cardTaps=%d",
                revision,
                buildNumber,
                d09CardTaps));
    }

    private void updateD10State() {
        if (currentD10State == null) {
            return;
        }
        currentD10State.setText(String.format(
                Locale.ROOT,
                "scene=d10 revision=%d build=%d parentTaps=%d childTaps=%d childLongTaps=%d badgeTaps=%d",
                revision,
                buildNumber,
                d10ParentTaps,
                d10ChildTaps,
                d10ChildLongTaps,
                d10BadgeTaps));
    }

    private String inputBDescription() {
        if ("alternate-description".equals(d04Source)) {
            return "D04 alternate input anchor B";
        }
        if ("missing-description".equals(d04Source)) {
            return null;
        }
        if ("same-description".equals(d04Source)
                || "duplicate-anchor".equals(d04Source)) {
            return "D04 input anchor A";
        }
        return "D04 input anchor B";
    }

    private EditText inputField(int id, String description, String hint, final boolean inputA) {
        EditText input = new EditText(this);
        input.setId(id);
        input.setContentDescription(description);
        input.setHint(hint);
        input.setInputType(InputType.TYPE_CLASS_TEXT);
        input.setSingleLine(true);
        input.setTextSize(18);
        input.setSelectAllOnFocus(false);
        input.setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_YES);
        input.addTextChangedListener(new TextWatcher() {
            @Override
            public void beforeTextChanged(CharSequence text, int start, int count, int after) {
            }

            @Override
            public void onTextChanged(CharSequence text, int start, int before, int count) {
            }

            @Override
            public void afterTextChanged(Editable editable) {
                if (inputA) {
                    d04TextEditsA++;
                } else {
                    d04TextEditsB++;
                }
                updateD04State();
            }
        });
        input.setOnFocusChangeListener((view, hasFocus) -> updateD04State());
        return input;
    }

    private void applyD04FocusAndKeyboard(
            final boolean applyFocus,
            final boolean applyKeyboard) {
        View decor = getWindow().getDecorView();
        decor.post(() -> {
            EditText inputA = currentD04InputA;
            EditText inputB = currentD04InputB;
            if (inputA == null || inputB == null) {
                return;
            }

            EditText target = null;
            if (applyFocus) {
                if ("a".equals(d04Focus)) {
                    target = inputA;
                } else if ("b".equals(d04Focus)) {
                    target = inputB;
                }
                if (target == null) {
                    View focused = getCurrentFocus();
                    if (focused != null) {
                        focused.clearFocus();
                    }
                } else {
                    target.requestFocus();
                    target.setSelection(target.length());
                }
            } else {
                View focused = getCurrentFocus();
                if (focused == inputA) {
                    target = inputA;
                } else if (focused == inputB) {
                    target = inputB;
                }
            }

            if (applyKeyboard) {
                InputMethodManager inputMethodManager =
                        (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
                if ("shown".equals(d04Keyboard) && target != null) {
                    getWindow().setSoftInputMode(
                            WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE
                                    | WindowManager.LayoutParams.SOFT_INPUT_STATE_ALWAYS_VISIBLE);
                    inputMethodManager.showSoftInput(target, InputMethodManager.SHOW_IMPLICIT);
                } else {
                    View tokenView = target == null ? getCurrentFocus() : target;
                    if (tokenView != null) {
                        inputMethodManager.hideSoftInputFromWindow(tokenView.getWindowToken(), 0);
                    }
                }
            }
            updateD04State();
        });
    }

    private void updateD01State() {
        if (currentD01State == null) {
            return;
        }
        currentD01State.setText(String.format(
                Locale.ROOT,
                "scene=d01 revision=%d build=%d emptyViews=%d semanticTaps=%d",
                revision,
                buildNumber,
                d01EmptyCount,
                d01SemanticTaps));
    }

    private void updateD02State() {
        if (currentD02State == null) {
            return;
        }
        currentD02State.setText(String.format(
                Locale.ROOT,
                "scene=d02 revision=%d build=%d window=%s body=%s wrapper=%s",
                revision,
                buildNumber,
                d02Window,
                d02Body,
                d02Wrapper ? "present" : "absent"));
    }

    private void updateD03State() {
        if (currentD03State == null) {
            return;
        }
        currentD03State.setText(String.format(
                Locale.ROOT,
                "scene=d03 revision=%d build=%d variant=%s scroll=%s hint=%s itemTaps=%d blockerTaps=%d",
                revision,
                buildNumber,
                d03Variant,
                d03Scroll,
                d03Hint,
                d03ItemTaps,
                d03BlockerTaps));
    }

    private void updateD04State() {
        if (currentD04State == null) {
            return;
        }
        String focused = "none";
        if (currentD04InputA != null && currentD04InputA.hasFocus()) {
            focused = "a";
        } else if (currentD04InputB != null && currentD04InputB.hasFocus()) {
            focused = "b";
        }
        currentD04State.setText(String.format(
                Locale.ROOT,
                "scene=d04 revision=%d build=%d focus=%s keyboard=%s source=%s editsA=%d editsB=%d",
                revision,
                buildNumber,
                focused,
                d04ImeVisible ? "visible" : "hidden",
                d04Source,
                d04TextEditsA,
                d04TextEditsB));
    }

    private LinearLayout baseRoot(int id) {
        LinearLayout root = new LinearLayout(this);
        root.setId(id);
        root.setOrientation(LinearLayout.VERTICAL);
        final int baseLeft = dp(16);
        final int baseTop = dp(20);
        final int baseRight = dp(16);
        final int baseBottom = dp(16);
        root.setPadding(baseLeft, baseTop, baseRight, baseBottom);
        root.setOnApplyWindowInsetsListener((view, insets) -> {
            int systemTop = insets.getInsets(WindowInsets.Type.systemBars()).top;
            int systemBottom = insets.getInsets(WindowInsets.Type.systemBars()).bottom;
            int keyboardBottom = insets.getInsets(WindowInsets.Type.ime()).bottom;
            boolean imeVisible = insets.isVisible(WindowInsets.Type.ime());
            view.setPadding(
                    baseLeft,
                    baseTop + systemTop,
                    baseRight,
                    baseBottom + Math.max(systemBottom, keyboardBottom));
            if (d04ImeVisible != imeVisible) {
                d04ImeVisible = imeVisible;
                updateD04State();
            }
            return insets;
        });
        root.requestApplyInsets();
        root.setBackgroundColor(COLOR_BACKGROUND);
        return root;
    }

    private TextView heading(int id, String text) {
        TextView heading = new TextView(this);
        heading.setId(id);
        heading.setText(text);
        heading.setTextSize(24);
        heading.setTextColor(COLOR_ACCENT);
        heading.setPadding(0, 0, 0, dp(8));
        return heading;
    }

    private TextView stateText(int id) {
        TextView state = new TextView(this);
        state.setId(id);
        state.setTextSize(13);
        state.setTextColor(COLOR_TEXT);
        state.setPadding(dp(8), dp(6), dp(8), dp(6));
        state.setBackgroundColor(Color.rgb(232, 236, 240));
        return state;
    }

    private TextView fieldLabel(int id, String text) {
        TextView label = new TextView(this);
        label.setId(id);
        label.setText(text);
        label.setTextSize(15);
        label.setTextColor(COLOR_TEXT);
        return label;
    }

    private TextView plainText(String text) {
        TextView view = new TextView(this);
        view.setText(text);
        view.setTextSize(13);
        view.setTextColor(COLOR_MUTED);
        return view;
    }

    private LinearLayout.LayoutParams verticalParams(int height, int topMarginDp) {
        LinearLayout.LayoutParams params =
                new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, height);
        params.topMargin = dp(topMarginDp);
        return params;
    }

    private int dp(int value) {
        return Math.round(value * getResources().getDisplayMetrics().density);
    }

    private static boolean isOneOf(String value, String... allowed) {
        if (value == null) {
            return false;
        }
        for (String candidate : allowed) {
            if (candidate.equals(value)) {
                return true;
            }
        }
        return false;
    }
}
