package android

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// rootDisplayMain is the fixed root Binder helper entry point in the App.
const rootDisplayMain = ".RootDisplayMain"

// controlUIDMin and controlUIDMax bound the accepted application UID range.
const (
	controlUIDMin = 10000
	controlUIDMax = 100000
)

// uuidPattern is the RFC 4122 textual form accepted for helper identities.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// baseAPKPattern matches a `pm path` record for a base APK.
var baseAPKPattern = regexp.MustCompile(`^package:(/\S*/base\.apk)$`)

// StartDisplayHelper bootstraps the App's fixed root Binder helper for one
// create attempt. It validates the package/uid and never logs shell output.
//
// Both identities must be UUIDs; the helper rejects a caller whose UID is not
// the App's, and the nonce binds this attempt to its queued create command.
// Shell completion is not proof that a display exists.
func (d *Device) StartDisplayHelper(ctx context.Context, commandID, appInstanceID string) error {
	if d == nil {
		return errNilDevice
	}
	if !uuidPattern.MatchString(commandID) || !uuidPattern.MatchString(appInstanceID) {
		return newErr(CodeInvalidArgs, "display helper identities must be UUIDs", nil)
	}
	started := time.Now()
	apk, uid, err := d.resolveControlApp(ctx)
	if err != nil {
		d.logEvent(eventlog.Warn, "android.display_helper.rejected",
			eventlog.Str("code", CodeOf(err)),
			eventlog.Duration("ms", time.Since(started)))
		return err
	}
	uidText := strconv.Itoa(uid)
	// Detach the root display helper from the one-shot adb shell: the display
	// belongs to the paired App, and must survive a device-service crash so the
	// recovery barrier can observe and close it through the same App instance.
	bootstrap := "CLASSPATH=" + shellQuote(apk) +
		" nohup setsid /system/bin/app_process / " + d.controlPackage + rootDisplayMain +
		" " + commandID + " " + appInstanceID + " " + uidText +
		" </dev/null >/dev/null 2>&1 &"
	result, err := d.shellWith(ctx, d.helperTimeout(), "su -c "+shellQuote(bootstrap))
	if err != nil {
		d.logEvent(eventlog.Warn, "android.display_helper.rejected",
			eventlog.Str("code", CodeOf(err)),
			eventlog.Duration("ms", time.Since(started)))
		return err
	}
	if result.ExitCode != 0 {
		// Raw su/package-manager output can carry device and user data; it is
		// deliberately discarded.
		d.logEvent(eventlog.Warn, "android.display_helper.rejected",
			eventlog.Str("code", CodeHelper),
			eventlog.Duration("ms", time.Since(started)))
		return newErr(CodeHelper, "the display helper bootstrap failed", nil)
	}
	d.logEvent(eventlog.Info, "android.display_helper.started",
		eventlog.Str("command_id", commandID),
		eventlog.Duration("ms", time.Since(started)))
	return nil
}

// helperTimeout allows the first package-manager reads after a boot.
func (d *Device) helperTimeout() time.Duration {
	if d.timeout < 20*time.Second {
		return 20 * time.Second
	}
	return d.timeout
}

// resolveControlApp resolves exactly one base APK and exactly one application
// UID for the control package. The package filter of list-packages is a
// substring search, so records are matched exactly after trimming.
func (d *Device) resolveControlApp(ctx context.Context) (string, int, error) {
	d.mu.Lock()
	cachedPath, cachedUID, cachedAt := d.helperA.path, d.helperA.uid, d.helperFetchedAt
	d.mu.Unlock()
	if cachedPath != "" && cachedUID != 0 && time.Since(cachedAt) < time.Minute {
		return cachedPath, cachedUID, nil
	}
	paths, err := d.shellWith(ctx, d.timeout, "pm path --user 0 "+d.controlPackage)
	if err != nil {
		return "", 0, newErr(CodeHelper, "the control App package identity is unavailable", nil)
	}
	if paths.ExitCode != 0 {
		return "", 0, newErr(CodeHelper, "the control App package identity is unavailable", nil)
	}
	packages, err := d.shellWith(ctx, d.timeout,
		"cmd package list packages -U --user 0 "+d.controlPackage)
	if err != nil {
		return "", 0, newErr(CodeHelper, "the control App package identity is unavailable", nil)
	}
	if packages.ExitCode != 0 {
		return "", 0, newErr(CodeHelper, "the control App package identity is unavailable", nil)
	}
	apk, uid, parseErr := parseControlIdentity(
		string(paths.Stdout), string(packages.Stdout), d.controlPackage)
	if parseErr != nil {
		return "", 0, parseErr
	}
	d.mu.Lock()
	d.helperA.path, d.helperA.uid, d.helperFetchedAt = apk, uid, time.Now()
	d.mu.Unlock()
	return apk, uid, nil
}

// parseControlIdentity applies the exact-identity rules: one base APK and one
// UID inside the application range. The UID is the last token of the record so
// a `package:<name>.debug uid:<n>` line never satisfies the filter.
func parseControlIdentity(pathOutput, packageOutput, controlPackage string) (string, int, error) {
	var apks []string
	for _, line := range strings.Split(pathOutput, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if match := baseAPKPattern.FindStringSubmatch(line); match != nil {
			apks = append(apks, match[1])
		}
	}
	var uids []int
	prefix := "package:" + controlPackage + " "
	for _, line := range strings.Split(packageOutput, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, prefix))
		if len(fields) != 1 || !strings.HasPrefix(fields[0], "uid:") {
			continue
		}
		uid, err := strconv.Atoi(strings.TrimPrefix(fields[0], "uid:"))
		if err != nil {
			continue
		}
		uids = append(uids, uid)
	}
	if len(apks) != 1 || len(uids) != 1 {
		return "", 0, newErr(CodeHelper, "the control App package identity is ambiguous", nil)
	}
	if uids[0] < controlUIDMin || uids[0] >= controlUIDMax {
		return "", 0, newErr(CodeHelper, "the control App UID is outside the application range", nil)
	}
	return apks[0], uids[0], nil
}

// shellQuote wraps one device-side token in single quotes, escaping embedded
// quotes so the device shell receives it literally.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
