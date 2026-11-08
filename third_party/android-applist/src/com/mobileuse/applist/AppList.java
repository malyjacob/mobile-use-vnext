package com.mobileuse.applist;

import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.os.Looper;
import android.util.Base64;

import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Print the launchable applications of the current user to stdout.
 *
 * <p>Each line is {@code package<TAB>base64(UTF-8 label)}. Base64 avoids any
 * ambiguity from tabs, newlines or non-ASCII characters in an application label.
 * The process runs through app_process as the shell user, so it needs no APK and
 * no package-visibility declaration. Callers must treat this output as untrusted
 * text: the label is user-facing content and must never enter diagnostic logs.
 */
public final class AppList {
    private AppList() {
    }

    public static void main(String[] args) throws Exception {
        // ActivityThread.systemMain() installs a Handler; a Looper must exist first.
        Looper.prepare();
        Class<?> activityThread = Class.forName("android.app.ActivityThread");
        Object thread = activityThread.getMethod("systemMain").invoke(null);
        Context context = (Context) activityThread.getMethod("getSystemContext").invoke(thread);
        PackageManager manager = context.getPackageManager();

        Intent launcher = new Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER);
        List<ResolveInfo> activities = manager.queryIntentActivities(launcher, 0);
        Map<String, String> applications = new LinkedHashMap<>();
        for (ResolveInfo activity : activities) {
            if (activity.activityInfo == null || activity.activityInfo.applicationInfo == null) {
                continue;
            }
            String packageName = activity.activityInfo.applicationInfo.packageName;
            if (applications.containsKey(packageName)) {
                continue;
            }
            CharSequence label = activity.loadLabel(manager);
            applications.put(packageName, label == null ? "" : label.toString());
        }

        List<Map.Entry<String, String>> sorted = new ArrayList<>(applications.entrySet());
        sorted.sort(
                Comparator.comparing(
                                (Map.Entry<String, String> entry) -> entry.getValue(),
                                String.CASE_INSENSITIVE_ORDER)
                        .thenComparing(Map.Entry::getKey));

        StringBuilder output = new StringBuilder(sorted.size() * 64);
        for (Map.Entry<String, String> entry : sorted) {
            output.append(entry.getKey())
                    .append('\t')
                    .append(Base64.encodeToString(
                            entry.getValue().getBytes(StandardCharsets.UTF_8),
                            Base64.NO_WRAP))
                    .append('\n');
        }
        System.out.print(output);
    }
}
