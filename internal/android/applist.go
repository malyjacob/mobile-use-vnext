package android

import (
	"context"
	"encoding/base64"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// The app discovery helper is a fixed app_process dex. It runs as the shell
// user, needs no APK and no package-visibility declaration. Labels cross the
// transport as base64 and are user-facing content; they must never be logged.
const (
	// AppListDexSHA256 pins the checked-in helper artifact.
	AppListDexSHA256 = "63818f32f7263d6be9136b2c5cb08c32e6eb1473c10ca1908f63f98c1f94dba8"
	// defaultRemoteAppListDex is the fixed device copy path.
	defaultRemoteAppListDex = "/data/local/tmp/mobile-use-applist.dex"
	// appListMainClass is the helper entry point.
	appListMainClass = "com.mobileuse.applist.AppList"
	// appListTimeout bounds one full discovery invocation.
	appListTimeout = 20 * time.Second
)

// appListEntry is one parsed helper line before it becomes a device fact.
type appListEntry struct {
	name    string
	pkg     string
	sortKey string
}

// EnsureAppList verifies the pinned local helper and pushes it when the device
// copy is absent or differs. A stale remote copy is replaced, never trusted.
func (d *Device) EnsureAppList(ctx context.Context) error {
	if d == nil {
		return errNilDevice
	}
	if d.appListPath == "" {
		return newErr(CodeAvailability, "the app list helper is not configured", nil)
	}
	sum, err := hashFile(d.appListPath)
	if err != nil {
		return err
	}
	if sum != AppListDexSHA256 {
		return newErr(CodeArtifact, "the app list helper has an unexpected SHA-256", nil)
	}
	if d.remoteHashOf(ctx, d.remoteAppListPath) == AppListDexSHA256 {
		return nil
	}
	started := time.Now()
	exe, err := d.exePath()
	if err != nil {
		return err
	}
	result, err := d.run(ctx, 60*time.Second, exe,
		d.adbArgs("push", d.appListPath, d.remoteAppListPath)...)
	if err != nil || result.ExitCode != 0 {
		return newErr(CodeArtifact, "the app list helper could not be pushed", nil)
	}
	if d.remoteHashOf(ctx, d.remoteAppListPath) != AppListDexSHA256 {
		return newErr(CodeArtifact, "the pushed app list helper did not verify", nil)
	}
	d.logEvent(eventlog.Info, "android.applist.pushed",
		eventlog.Int("count", 1), eventlog.Duration("ms", time.Since(started)))
	return nil
}

// remoteHashOf reads one device copy's SHA-256, returning "" when it is absent
// or unreadable. A missing remote file is expected before the first push.
func (d *Device) remoteHashOf(ctx context.Context, path string) string {
	if path == "" {
		return ""
	}
	result, err := d.Shell(ctx, "sha256sum "+shellQuote(path))
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	sum := firstField(result.Stdout)
	if !isHex64(sum) {
		return ""
	}
	return strings.ToLower(sum)
}

// AppList reads the launchable applications of the current user. The helper
// output is device data: a malformed line is an integration failure, not a
// partial success, and no label or package is written to the event log.
func (d *Device) AppList(ctx context.Context) ([]device.AppEntry, error) {
	if d == nil {
		return nil, errNilDevice
	}
	started := time.Now()
	if err := d.EnsureAppList(ctx); err != nil {
		return nil, err
	}
	command := "CLASSPATH=" + shellQuote(d.remoteAppListPath) +
		" /system/bin/app_process /system/bin " + appListMainClass + " 2>/dev/null"
	result, err := d.shellWith(ctx, appListTimeout, command)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, newErr(CodeAvailability, "the app list helper failed", nil)
	}
	entries, err := parseAppList(result.Stdout)
	if err != nil {
		return nil, newErr(CodeAvailability, "the app list output was invalid", nil)
	}
	if len(entries) == 0 {
		return nil, newErr(CodeAvailability, "the app list was empty", nil)
	}
	out := make([]device.AppEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, device.AppEntry{Name: entry.name, Package: entry.pkg})
	}
	d.logEvent(eventlog.Info, "android.applist.read",
		eventlog.Int("count", len(out)), eventlog.Duration("ms", time.Since(started)))
	return out, nil
}

// parseAppList accepts only `package<TAB>base64(label)` lines. Duplicate
// packages are collapsed in helper order; the final order is deterministic by
// folded label and then package so equal labels stay stable.
func parseAppList(data []byte) ([]appListEntry, error) {
	seen := make(map[string]bool)
	entries := make([]appListEntry, 0, 64)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		pkg, encoded, ok := strings.Cut(line, "\t")
		if !ok || !protocol.ValidPackageName(pkg) {
			return nil, errInvalidAppList
		}
		labelBytes, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || !utf8.Valid(labelBytes) {
			return nil, errInvalidAppList
		}
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		label := string(labelBytes)
		entries = append(entries, appListEntry{
			name: label, pkg: pkg, sortKey: protocol.Fold(label),
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].sortKey != entries[j].sortKey {
			return entries[i].sortKey < entries[j].sortKey
		}
		return entries[i].pkg < entries[j].pkg
	})
	return entries, nil
}

// appListError is a controlled sentinel; its text never contains device data.
type appListError struct{}

func (appListError) Error() string { return "invalid app list output" }

var errInvalidAppList = appListError{}
