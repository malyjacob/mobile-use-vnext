package android

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Process-level tests run the real transport against a stubbed adb process, so
// argv shape, exit handling and parsing are exercised without a device.
//
// The stub is an identical copy of the running test binary plus a sentinel
// environment entry. When the sentinel is present the binary replays one
// recorded transcript instead of running the suite. The sentinel carries a
// short digest of the transcript name and never any user text.
const fakeSentinelEnv = "MOBILE_USE_ADB_STUB"
const fakeCallCountEnv = "MOBILE_USE_FAKE_ADB_CALLS"

// fakeTranscripts is every replay the tests can select.
var fakeTranscripts = []string{
	"argv-record", "exit3", "remote-hash", "screenshot", "input",
	"input-exit", "input-transport", "hang", "probe", "capture",
	"applist", "launch", "launch-rejected", "launch-exit",
	"launch-missing", "launch-transport",
	"focus-exit", "shell", "identity-comma", "identity-lines",
	"features-missing", "features-partial", "identity-bad-device",
	"identity-bad-boot",
	"task-move", "task-move-exit", "task-move-transport",
	"task-remove-exit", "task-remove-transport",
	"processes", "processes-empty", "processes-unreadable",
	"processes-ticks-changed", "processes-root", "processes-no-root",
	"processes-kill-no-root", "processes-kill-denied",
	"media-playing", "media-stopped", "media-unknown",
	"fgs-hit", "fgs-none", "fgs-unknown",
	"tasks", "tasks-incomplete",
	"package-identity", "package-identity-empty-signature",
	"package-identity-not-installed", "package-identity-user10",
	"package-identity-uid-mismatch", "package-identity-ambiguous",
	"package-processes-user10", "package-processes-identity-unknown",
	"force-stop", "force-stop-exit", "force-stop-transport",
}

// fakeDigestOf returns the sentinel value for one transcript.
func fakeDigestOf(name string) string {
	digest := sha256.Sum256([]byte(name))
	return hex.EncodeToString(digest[:])[:16]
}

// fakeTranscriptOf returns the transcript name for one sentinel value.
func fakeTranscriptOf(sentinel string) (string, bool) {
	for _, name := range fakeTranscripts {
		if fakeDigestOf(name) == sentinel {
			return name, true
		}
	}
	return "", false
}

// exeSuffix is the platform executable suffix.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// fakeEnv returns the environment for one stubbed invocation.
func fakeEnv(transcript string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, fakeSentinelEnv+"=") ||
			strings.HasPrefix(entry, "MOBILE_USE_FAKE_ADB_ARGV=") ||
			strings.HasPrefix(entry, fakeCallCountEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, fakeSentinelEnv+"="+fakeDigestOf(transcript))
}

// fakeADB stages the stub for one transcript and returns its path. A named
// launcher is used on POSIX so argv[0] reads like adb; Windows uses the copy.
func fakeADB(t *testing.T, transcript string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	raw, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "adb-"+fakeDigestOf(transcript)+exeSuffix())
	if err := os.WriteFile(path, raw, 0o755); err != nil {
		t.Fatalf("stage stub: %v", err)
	}
	return path
}

// TestMain dispatches a stubbed invocation before running the normal tests.
func TestMain(m *testing.M) {
	if sentinel := os.Getenv(fakeSentinelEnv); sentinel != "" {
		if transcript, ok := fakeTranscriptOf(sentinel); ok {
			os.Exit(runStub(transcript))
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// stubArgv is the parsed `adb -s <serial> <verb> <rest...>` invocation.
type stubArgv struct {
	serial string
	verb   string
	rest   []string
	ok     bool
}

// parseStubArgv reads one invocation.
func parseStubArgv() stubArgv {
	args := os.Args[1:]
	if len(args) >= 3 && args[0] == "-s" {
		return stubArgv{serial: args[1], verb: args[2], rest: args[3:], ok: true}
	}
	return stubArgv{}
}

// runStub replays the invocation and returns its exit status.
func runStub(transcript string) int {
	invocation := parseStubArgv()
	if path := os.Getenv("MOBILE_USE_FAKE_ADB_ARGV"); path != "" && invocation.verb == "shell" {
		_ = os.WriteFile(path, []byte(strings.Join(os.Args[1:], "\n")+"\n"), 0o644)
	}
	if path := os.Getenv(fakeCallCountEnv); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			_, _ = file.WriteString("1")
			_ = file.Close()
		}
	}
	if !invocation.ok {
		return 2
	}
	command := strings.Join(invocation.rest, " ")
	switch invocation.verb {
	case "push":
		if transcript, ok := fakeTranscriptOf(os.Getenv(fakeSentinelEnv)); ok && transcript == "probe" {
			_ = os.WriteFile(filepath.Join(os.TempDir(), "mobile-use-stub-"+fakeDigestOf(transcript)), nil, 0o600)
		}
		return 0
	case "get-state":
		_, _ = os.Stdout.WriteString("device\n")
		return 0
	case "features":
		switch transcript {
		case "identity-comma":
			_, _ = os.Stdout.WriteString("cmd,shell_v2,stat_v2\n")
		case "identity-lines":
			_, _ = os.Stdout.WriteString("cmd\nshell_v2\nstat_v2\n")
		case "features-missing":
			_, _ = os.Stdout.WriteString("cmd,stat_v2\n")
		case "features-partial":
			_, _ = os.Stdout.WriteString("cmd,shell_v2_extra\n")
		case "identity-bad-device", "identity-bad-boot":
			_, _ = os.Stdout.WriteString("shell_v2\n")
		default:
			_, _ = os.Stdout.WriteString("shell_v2\n")
		}
		return 0
	case "exec-out":
		if transcript == "shell" {
			_, _ = os.Stdout.WriteString("hi\n")
			return 0
		}
		_, _ = os.Stdout.Write(pngFixture)
		return 0
	case "shell":
		return replayShell(transcript, command)
	}
	return 2
}

// replayShell answers one shell command for one transcript.
func replayShell(transcript, command string) int {
	if answer, found := transcriptShell(transcript, command); found {
		_, _ = os.Stdout.WriteString(answer.stdout)
		_, _ = os.Stderr.WriteString(answer.stderr)
		return answer.exit
	}
	if transcript == "shell" {
		switch {
		case strings.Contains(command, "sha256sum"):
			_, _ = os.Stdout.WriteString(sha256Hex([]byte(runnerScript)) + "  " + defaultShellRunnerPath + "\n")
			return 0
		case strings.Contains(command, "wc -c <"):
			_, _ = os.Stdout.WriteString("3\n")
			return 0
		case strings.Contains(command, "cat >"):
			return 0
		case strings.Contains(command, "setsid"):
			return 0
		case strings.Contains(command, "version:"):
			_, _ = os.Stdout.WriteString("version:1\n" +
				"start:start_v1\t123\t123\t456\tboot\n" +
				"done:\npid:123\nalive:1\n")
			return 0
		case strings.Contains(command, "/proc/123/stat"):
			_, _ = os.Stdout.WriteString("456\n")
			return 0
		case strings.Contains(command, "kill -"):
			return 0
		case strings.Contains(command, "id -u"):
			_, _ = os.Stdout.WriteString("0\n")
			return 0
		case strings.Contains(command, "printf"):
			return 0
		}
		return 0
	}
	switch transcript {
	case "remote-hash":
		if strings.Contains(command, "missing.jar") {
			return 1
		}
		_, _ = os.Stdout.WriteString(U2JarSHA256 + "  /data/local/tmp/u2.jar\n")
		return 0
	case "probe":
		// The remote copy is stale until the artifact has been pushed, so the
		// push path runs and is then confirmed by this same command.
		if strings.HasPrefix(command, "sha256sum ") && os.Getenv("MOBILE_USE_STUB_STALE_REMOTE") == "" {
			_, _ = os.Stdout.WriteString(U2JarSHA256 + "  /data/local/tmp/u2.jar\n")
			return 0
		}
	case "exit3":
		_, _ = os.Stdout.WriteString("out\n")
		_, _ = os.Stderr.WriteString("err\n")
		return 3
	case "hang":
		time.Sleep(30 * time.Second)
		return 0
	}
	return 0
}

// stubAnswer is one recorded shell answer.
type stubAnswer struct {
	contains string
	stdout   string
	stderr   string
	exit     int
}

// stubTranscripts maps a transcript to its recorded answers.
var stubTranscripts = map[string][]stubAnswer{
	"launch": {
		{contains: "resolve-activity", stdout: "com.example.app/.MainActivity\n"},
		{contains: "am start", stdout: "Starting: Intent { cmp=com.example.app/.MainActivity }\nStatus: ok\nActivity: com.example.app/.MainActivity\n"},
		{contains: "am task focus", stdout: ""},
		{contains: "am stack remove", stdout: ""},
		{contains: "dumpsys activity recents", stdout: "  Recent #0: Task{abc #42 type=standard A=1:com.example.app}\n  Recent #1: Task{def #77 type=home}\n"},
		{contains: "am stack list", stdout: launchStackListing},
		{contains: "dumpsys activity activities", stdout: probeActivities},
	},
	"applist": {
		{contains: "sha256sum", stdout: AppListDexSHA256 + "  " + defaultRemoteAppListDex + "\n"},
		{contains: "com.mobileuse.applist.AppList", stdout: appListFixture},
	},
	"probe": {
		{contains: "-U --user 0", stdout: "package:" + defaultControlPackage + " uid:10354\n"},
		{contains: "pkill", exit: 1},
		{contains: "su -c", stdout: "Shell output must never be logged\n"},
		{contains: "SurfaceFlinger --display-id", stdout: probeSurfaceFlinger},
		{contains: "dumpsys display", stdout: probeDisplay},
		{contains: "dumpsys activity activities", stdout: probeActivities},
		{contains: "dumpsys window displays", stdout: probeWindows},
		{contains: "wm size", stdout: "Physical size: 1440x3200\nOverride size: 1080x2400\n"},
		{contains: "wm density", stdout: "Physical density: 600\nOverride density: 450\n"},
		{contains: "getprop ro.product.model", stdout: "stub-model\n"},
		{contains: "getprop ro.build.version.release", stdout: "16\n"},
		{contains: "getprop ro.build.version.sdk", stdout: "36\n"},
	},
	"capture": {
		{contains: "am stack list", stdout: probeStackListing},
		{contains: "dumpsys activity activities", stdout: probeActivities},
		{contains: "dumpsys window displays", stdout: probeWindows},
	},
	"identity-comma":      identityTranscript("device-test", testBootID),
	"identity-lines":      identityTranscript("device-test", testBootID),
	"identity-bad-device": identityTranscript("device test", testBootID),
	"identity-bad-boot":   identityTranscript("device-test", "not-a-boot-id"),
	"input-exit": {
		{contains: "input -d", stderr: "error: permission denied\n", exit: 7},
	},
	"input-transport": {
		{contains: "input -d", stderr: "adb: device offline\n", exit: 1},
	},
	"launch-rejected": {
		{contains: "am start", stdout: "Error: Activity not started\n"},
	},
	"launch-exit": {
		{contains: "am start", stderr: "error: launch denied\n", exit: 5},
	},
	"launch-missing": {
		{contains: "am start", stdout: "Activity: com.example.app/.MainActivity\n"},
	},
	"launch-transport": {
		{contains: "am start", stderr: "adb: device offline\n", exit: 1},
	},
	"focus-exit": {
		{contains: "am task focus", stderr: "error: task focus denied\n", exit: 5},
	},
	"task-move": {
		{contains: "am display move-stack", stdout: ""},
	},
	"task-move-exit": {
		{contains: "am display move-stack", stderr: "error: move denied\n", exit: 4},
	},
	"task-move-transport": {
		{contains: "am display move-stack", stderr: "adb: device offline\n", exit: 1},
	},
	"task-remove-exit": {
		{contains: "am stack remove", stderr: "error: removal denied\n", exit: 3},
	},
	"task-remove-transport": {
		{contains: "am stack remove", stderr: "adb: device offline\n", exit: 1},
	},
	"processes": {
		{contains: "cat /proc/sys/kernel/random/boot_id", stdout: testBootID + "\n"},
		{contains: "dumpsys package ", stdout: standardPackageDump},
		{contains: "list packages -U", stdout: "package:com.example.app uid:10140\n"},
		{contains: "ps -A -o PID,UID,NAME", stdout: processListingFixture},
		{contains: "for p in 101 202; do", stdout: processTicksFixture},
		{contains: "for p in 101; do",
			stdout: "stat " + processStatFixture(101, "com.example.app", 98765) + "\n"},
		{contains: "grep '^Name:'", stdout: "com.example.app\n10140\n"},
		{contains: "su -c 'kill -9", stdout: ""},
	},
	"processes-kill-no-root": {
		{contains: "for p in 101; do", stdout: "stat 101 -\n"},
		{contains: "su -c 'for p in 101; do", stderr: "su: not found\n", exit: 127},
	},
	"processes-kill-denied": {
		{contains: "for p in 101; do",
			stdout: "stat " + processStatFixture(101, "com.example.app", 98765) + "\n"},
		{contains: "grep '^Name:'", stdout: "com.example.app\n10140\n"},
		{contains: "su -c 'kill -9", stderr: "su: permission denied\n", exit: 1},
	},
	"processes-empty": {
		{contains: "cat /proc/sys/kernel/random/boot_id", stdout: testBootID + "\n"},
		{contains: "dumpsys package ", stdout: standardPackageDump},
		{contains: "list packages -U", stdout: "package:com.example.app uid:10140\n"},
		{contains: "ps -A -o PID,UID,NAME", stdout: processHeaderFixture +
			"1   0     init\n"},
	},
	"processes-unreadable": {
		{contains: "cat /proc/sys/kernel/random/boot_id", stdout: testBootID + "\n"},
		{contains: "dumpsys package ", stdout: standardPackageDump},
		{contains: "list packages -U", stdout: "package records unavailable\n"},
	},
	"processes-ticks-changed": {
		{contains: "for p in 101; do", stdout: "stat " + processStatFixture(101,
			"com.example.app", 11111) + "\n"},
	},
	"processes-root": {
		{contains: "cat /proc/sys/kernel/random/boot_id", stdout: testBootID + "\n"},
		{contains: "dumpsys package ", stdout: standardPackageDump},
		{contains: "list packages -U", stdout: "package:com.example.app uid:10140\n"},
		{contains: "ps -A -o PID,UID,NAME", stdout: processListingFixture},
		// The direct read sees neither /proc entry; the root path reads both.
		{contains: "for p in 101 202; do", stdout: "stat 101 -\nstat 202 -\n"},
		{contains: "su -c 'for p in 101 202; do", stdout: processTicksFixture},
	},
	"processes-no-root": {
		{contains: "cat /proc/sys/kernel/random/boot_id", stdout: testBootID + "\n"},
		{contains: "dumpsys package ", stdout: standardPackageDump},
		{contains: "list packages -U", stdout: "package:com.example.app uid:10140\n"},
		{contains: "ps -A -o PID,UID,NAME", stdout: processListingFixture},
		{contains: "for p in 101 202; do", stdout: "stat 101 -\nstat 202 -\n"},
		{contains: "su -c 'for p in 101 202; do", stderr: "su: not found\n", exit: 127},
	},
	"media-playing": {
		{contains: "dumpsys media_session", stdout: mediaPlayingFixture},
	},
	"media-stopped": {
		{contains: "dumpsys media_session", stdout: mediaStoppedFixture},
	},
	"media-unknown": {
		{contains: "dumpsys media_session", stdout: "unexpected dump\n"},
	},
	"fgs-hit": {
		{contains: "dumpsys activity services", stdout: serviceForegroundFixture(true)},
	},
	"fgs-none": {
		{contains: "dumpsys activity services", stdout: serviceForegroundFixture(false)},
	},
	"fgs-unknown": {
		{contains: "dumpsys activity services", stdout: "unexpected services dump\n"},
	},
	"tasks": {
		{contains: "am stack list", stdout: tasksListing},
	},
	"tasks-incomplete": {
		{contains: "am stack list", stdout: incompleteTasksListing},
	},
	"package-identity": {
		{contains: "dumpsys package ", stdout: dumpsysPackageFixture("com.example.app", 10388,
			"fc909ae5", nil)},
		{contains: "list packages -U --user 0", stdout: "package:com.example.app uid:10388\n"},
	},
	"package-identity-empty-signature": {
		{contains: "dumpsys package ", stdout: dumpsysPackageFixture("com.example.app", 10388,
			"", nil)},
		{contains: "list packages -U --user 0", stdout: "package:com.example.app uid:10388\n"},
	},
	"package-identity-not-installed": {
		{contains: "dumpsys package ", stdout: dumpsysPackageFixture("com.example.app", 10388,
			"fc909ae5", []fakeInstalledUser{{ID: 0, Installed: false,
				FirstInstall: "2026-11-09 03:45:47"}})},
		{contains: "list packages -U --user 0", stdout: "package:com.example.app uid:10388\n"},
	},
	"package-identity-user10": {
		{contains: "dumpsys package ", stdout: dumpsysPackageFixture("com.example.app", 10317,
			"fc909ae5", []fakeInstalledUser{
				{ID: 0, Installed: true, FirstInstall: "2026-08-04 19:53:47"},
				{ID: 10, Installed: true, FirstInstall: "2026-11-10 10:10:10"},
			})},
		{contains: "list packages -U --user 10", stdout: "package:com.example.app uid:1010317\n"},
	},
	"package-identity-uid-mismatch": {
		{contains: "dumpsys package ", stdout: dumpsysPackageFixture("com.example.app", 10317,
			"fc909ae5", []fakeInstalledUser{{ID: 10, Installed: true,
				FirstInstall: "2026-11-10 10:10:10"}})},
		// The user-10 listing reports user 0's full uid: the identity is never
		// assembled from a mismatching record.
		{contains: "list packages -U --user 10", stdout: "package:com.example.app uid:10317\n"},
	},
	"package-identity-ambiguous": {
		{contains: "dumpsys package ", stdout: ambiguousPackageFixture("com.example.app", 10388)},
		{contains: "list packages -U --user 0", stdout: "package:com.example.app uid:10388\n"},
	},
	"package-processes-user10": {
		{contains: "cat /proc/sys/kernel/random/boot_id", stdout: testBootID + "\n"},
		{contains: "dumpsys package ", stdout: dumpsysPackageFixture("com.example.app", 10317,
			"fc909ae5", []fakeInstalledUser{
				{ID: 0, Installed: true, FirstInstall: "2026-08-04 19:53:47"},
				{ID: 10, Installed: true, FirstInstall: "2026-11-10 10:10:10"},
			})},
		{contains: "list packages -U --user 10", stdout: "package:com.example.app uid:1010317\n"},
		{contains: "ps -A -o PID,UID,NAME", stdout: userProcessListingFixture},
		{contains: "for p in 101 202; do", stdout: userProcessTicksFixture},
	},
	"package-processes-identity-unknown": {
		{contains: "cat /proc/sys/kernel/random/boot_id", stdout: testBootID + "\n"},
		{contains: "dumpsys package ", stdout: dumpsysPackageFixture("com.example.app", 10388,
			"", nil)},
		{contains: "list packages -U --user 0", stdout: "package:com.example.app uid:10388\n"},
		{contains: "ps -A -o PID,UID,NAME", stdout: processListingFixture},
	},
	"force-stop": {
		{contains: "am force-stop", stdout: ""},
	},
	"force-stop-exit": {
		{contains: "am force-stop", stderr: "Error: force stop denied\n", exit: 5},
	},
	"force-stop-transport": {
		{contains: "am force-stop", stderr: "adb: device offline\n", exit: 1},
	},
}

const testBootID = "01234567-89ab-4cde-8fab-0123456789ab"

func identityTranscript(deviceID, bootID string) []stubAnswer {
	return []stubAnswer{
		{contains: "getprop ro.serialno", stdout: deviceID + "\n"},
		{contains: "/proc/sys/kernel/random/boot_id", stdout: bootID + "\n"},
	}
}

// transcriptShell returns the recorded answer for command, preferring the
// longest matching substring.
func transcriptShell(transcript, command string) (stubAnswer, bool) {
	best := stubAnswer{}
	found := false
	bestLength := -1
	for _, reply := range stubTranscripts[transcript] {
		if strings.Contains(command, reply.contains) && len(reply.contains) > bestLength {
			best, found, bestLength = reply, true, len(reply.contains)
		}
	}
	return best, found
}

// appListFixture is the helper's package<TAB>base64(label) output.
var appListFixture = "com.example.two\t" +
	base64.StdEncoding.EncodeToString([]byte("Example")) + "\n" +
	"com.example.one\t" +
	base64.StdEncoding.EncodeToString([]byte("示例")) + "\n" +
	"com.example.two\t" +
	base64.StdEncoding.EncodeToString([]byte("Duplicate")) + "\n"

// pngFixture is the exec-out payload used to prove the screenshot path.
var pngFixture = append([]byte("\x89PNG\r\n\x1a\n"), []byte("stub-bytes")...)

// stubbed outputs for the read-only commands.
const (
	probeSurfaceFlinger = "Display 4630946540531149187 (HWC display 0): port=131 pnpId=QCM displayName=\"\"\n" +
		"Display 1152921504722368738 (Virtual display): displayName=\"MobileUseAgent\"\n"
	probeDisplay = "Display Devices:\n  mDisplayId=0\n" +
		"    mOverrideDisplayInfo=DisplayInfo{\"built-in\", displayId 0, real 1080 x 2400, rotation 0, state ON, type INTERNAL}\n" +
		"  mDisplayId=61\n"
	probeActivities = "\nDisplay #0 (activities from top to bottom):\n" +
		"  * Task{c9eda97 #2 type=home U=0 visible=true}\n" +
		"    topResumedActivity=ActivityRecord{1 u0 com.example.home/.Launcher t2}\n" +
		"Display #61 (activities from top to bottom):\n" +
		"  * Task{bb #2120 type=standard A=1:com.example.app U=0 visible=true}\n" +
		"    topResumedActivity=ActivityRecord{2 u0 com.example.app/.Main t2120}\n"
	probeWindows = "WINDOW MANAGER DISPLAY CONTENTS (dumpsys window displays)\n" +
		"  Display: mDisplayId=0 (organized)\n" +
		"    mCurrentFocus=Window{61d0ce2 u0 com.example.home/.Launcher}\n"
	probeStackListing = "RootTask id=42 bounds=[0,0][1080,2400] displayId=0 userId=0\n" +
		"  taskId=42: com.example/com.example.Main bounds=[0,0][1080,2400] userId=0 visible=true\n"
	launchStackListing = "RootTask id=2120 bounds=[0,0][1080,2400] displayId=61 userId=0\n" +
		"  taskId=2120: com.example.app/com.example.app.Main bounds=[0,0][1080,2400] userId=0 visible=true\n"
	captureXML = `<hierarchy rotation="0">` +
		`<node class="android.widget.FrameLayout" package="com.example.app" display-id="0" visible-to-user="true" bounds="[0,0][1080,2400]">` +
		`<node class="android.widget.EditText" package="com.example.app" text="Query" resource-id="com.example.app:id/q" focused="true" clickable="true" enabled="true" visible-to-user="true" bounds="[0,100][1080,220]" />` +
		`</node>` +
		`<node class="android.widget.TextView" package="com.example.app" text="Other display" display-id="61" visible-to-user="true" bounds="[0,0][1080,2400]" />` +
		`</hierarchy>`
)

// Process fixtures reproduce the measured column shape of the process, media and
// service reads. Only placeholder packages appear.

// processHeaderFixture is the fixed column header of `ps -A -o PID,UID,NAME`.
const processHeaderFixture = "PID UID   NAME\n"

// processListingFixture holds one target package's main process, its :remote
// child and one unrelated entry.
var processListingFixture = processHeaderFixture +
	"1   0     init\n" +
	"101 10140 com.example.app\n" +
	"202 10140 com.example.app:remote\n" +
	"303 10141 com.example.other\n"

// processStatFixture renders one /proc/<pid>/stat line with the given start tick
// (field 22). The comm is parenthesised like the kernel prints it.
func processStatFixture(pid int, comm string, startTicks uint64) string {
	return strconv.Itoa(pid) + " (" + comm + ") S 1 101 101 0 -1 4194624 1234 0 0 0 " +
		"10 20 0 0 20 0 8 0 " + strconv.FormatUint(startTicks, 10) + " 12345678 4096"
}

// processTicksFixture is the batched start-tick read of the two target PIDs.
var processTicksFixture = "stat " + processStatFixture(101, "com.example.app", 98765) + "\n" +
	"stat " + processStatFixture(202, "com.example.app:remote", 98800) + "\n"

// mediaPlayingFixture carries one PLAYING PlaybackState record.
const mediaPlayingFixture = "MEDIA SESSION SERVICE (dumpsys media_session)\n" +
	"\n" +
	"MediaSessionService:\n" +
	"  Sessions Stack - have 1 sessions:\n" +
	"    * SessionRecord{a1b2c3d}\n" +
	"      package=com.example.app\n" +
	"      state=PlaybackState {state=3, position=1000, speed=1.0, updated=1700000000}\n"

// mediaStoppedFixture carries one paused PlaybackState record.
const mediaStoppedFixture = "MEDIA SESSION SERVICE (dumpsys media_session)\n" +
	"\n" +
	"MediaSessionService:\n" +
	"  Sessions Stack - have 1 sessions:\n" +
	"    * SessionRecord{a1b2c3d}\n" +
	"      package=com.example.app\n" +
	"      state=PlaybackState {state=2, position=1000, speed=0.0, updated=1700000000}\n"

// serviceForegroundFixture renders one ServiceRecord for the target package.
func serviceForegroundFixture(foreground bool) string {
	flag, notificationID := "false", "0"
	if foreground {
		flag, notificationID = "true", "1234"
	}
	return "ACTIVITY MANAGER SERVICES (dumpsys activity services)\n" +
		"  User 0 active services:\n" +
		"  * ServiceRecord{a1b2c3d u0 com.example.app/.MusicService}\n" +
		"    intent={cmp=com.example.app/.MusicService}\n" +
		"    packageName=com.example.app\n" +
		"    processName=com.example.app\n" +
		"    isForeground=" + flag + "\n" +
		"    foregroundId=" + notificationID + "\n"
}

// Package-identity fixtures reproduce the measured `dumpsys package` shape of
// Android 16: the record lives in the `Packages:` section and a later `Queries:`
// section repeats an appId and a user record, so a parser that ignores the
// section boundary is caught.

// fakeInstalledUser is one `User <id>:` block of a stubbed package dump.
type fakeInstalledUser struct {
	ID           int
	Installed    bool
	FirstInstall string
}

// dumpsysPackageFixture renders one package dump with the given installed-user
// blocks. A second section repeats the first user so section-bounded parsing is
// exercised by every transcript built from it.
func dumpsysPackageFixture(pkg string, appID int, signature string, users []fakeInstalledUser) string {
	if len(users) == 0 {
		users = []fakeInstalledUser{{ID: 0, Installed: true, FirstInstall: "2026-11-09 03:45:47"}}
	}
	var builder strings.Builder
	builder.WriteString("Activity Resolver Table:\n")
	builder.WriteString("  Non-Data Actions:\n")
	builder.WriteString("      android.intent.action.MAIN:\n")
	builder.WriteString("        a1b2c3 " + pkg + "/.Main\n")
	builder.WriteString("\nDomain verification status:\n")
	builder.WriteString("\nKey Set Manager:\n")
	builder.WriteString("  [" + pkg + "]\n")
	builder.WriteString("      Signing KeySets: 1\n")
	builder.WriteString("\nPackages:\n")
	writePackageRecord(&builder, pkg, appID, signature)
	for _, user := range users {
		writePackageUser(&builder, pkg, user)
	}
	builder.WriteString("\nQueries:\n")
	builder.WriteString("  " + pkg + ":\n")
	builder.WriteString("    appId=" + strconv.Itoa(appID) + "\n")
	builder.WriteString("    User " + strconv.Itoa(users[0].ID) + ":\n")
	return builder.String()
}

// writePackageRecord renders one `Package [<pkg>] (<hex>):` section record.
func writePackageRecord(builder *strings.Builder, pkg string, appID int, signature string) {
	builder.WriteString("  Package [" + pkg + "] (f07413c):\n")
	builder.WriteString("    appId=" + strconv.Itoa(appID) + "\n")
	builder.WriteString("    pkg=Package{29bd3c2 " + pkg + "}\n")
	builder.WriteString("    codePath=/data/app/~~a==/" + pkg + "-b==\n")
	builder.WriteString("    versionCode=37 minSdk=26 targetSdk=36\n")
	builder.WriteString("    flags=[ DEBUGGABLE HAS_CODE ALLOW_CLEAR_USER_DATA ALLOW_BACKUP ]\n")
	builder.WriteString("    install permissions:\n")
	builder.WriteString("      android.permission.INTERNET: granted=true\n")
	builder.WriteString("    signatures=PackageSignatures{5346d19 version:3, signatures:[" +
		signature + "], past signatures:[8f53736c]}\n")
}

// writePackageUser renders one per-user block with a deeper firstInstallTime.
func writePackageUser(builder *strings.Builder, pkg string, user fakeInstalledUser) {
	installed := "false"
	if user.Installed {
		installed = "true"
	}
	id := strconv.Itoa(user.ID)
	builder.WriteString("    User " + id +
		": ceDataInode=198549 deDataInode=450607 installed=" + installed +
		" hidden=false suspended=false distractionFlags=0 stopped=true notLaunched=false" +
		" enabled=0 instant=false virtual=false quarantined=false\n")
	builder.WriteString("      installReason=0\n")
	builder.WriteString("      dataDir=/data/user/" + id + "/" + pkg + "\n")
	builder.WriteString("      firstInstallTime=" + user.FirstInstall + "\n")
	builder.WriteString("      uninstallReason=0\n")
	builder.WriteString("      gids=[3003]\n")
}

// ambiguousPackageFixture repeats one package record inside the Packages
// section so the record count is not exactly one.
func ambiguousPackageFixture(pkg string, appID int) string {
	var builder strings.Builder
	builder.WriteString("Packages:\n")
	writePackageRecord(&builder, pkg, appID, "fc909ae5")
	writePackageUser(&builder, pkg, fakeInstalledUser{ID: 0, Installed: true,
		FirstInstall: "2026-11-09 03:45:47"})
	writePackageRecord(&builder, pkg, appID, "fc909ae5")
	writePackageUser(&builder, pkg, fakeInstalledUser{ID: 0, Installed: true,
		FirstInstall: "2026-11-09 03:45:47"})
	return builder.String()
}

// tasksListing reproduces the measured `am stack list` shape: a root segment per
// container, one leaf per task, and one unknown leaf that still belongs to a
// container.
const tasksListing = "RootTask id=297 bounds=[0,0][1080,2400] displayId=0 userId=0\n" +
	" configuration={1.0 460mcc1mnc [en_US] ldltr sw384dp w384dp h853dp 450dpi}\n" +
	"  taskId=297: com.example.app/com.example.app.Main bounds=[0,0][1080,2400] userId=0 visible=true topActivity=ComponentInfo{com.example.app/.Main}\n" +
	"\n" +
	"RootTask id=3 bounds=[0,0][1080,2400] displayId=0 userId=0\n" +
	"  taskId=4: unknown bounds=[0,0][1080,2400] userId=0 visible=false\n" +
	"\n" +
	"RootTask id=88 bounds=[0,0][720,1280] displayId=61 userId=10\n" +
	"  taskId=88: com.example.app/com.example.app.Main bounds=[0,0][720,1280] userId=10 visible=true\n"

// incompleteTasksListing carries one leaf before any root segment, so the
// enumeration cannot be trusted even though the readable task is returned.
const incompleteTasksListing = "  taskId=7: com.example.app/.Main visible=true\n" +
	"RootTask id=297 bounds=[0,0][1080,2400] displayId=0 userId=0\n" +
	"  taskId=297: com.example.app/.Main visible=true\n"

// userProcessListingFixture holds the same package under user 0 and user 10 plus
// one unrelated entry of user 10. Only the user-10 full uid is the target.
var userProcessListingFixture = processHeaderFixture +
	"1   0       init\n" +
	"101 1010317 com.example.app\n" +
	"202 1010317 com.example.app:remote\n" +
	"303 10317   com.example.app\n" +
	"404 1010318 com.example.other\n"

// userProcessTicksFixture is the batched start-tick read of the two user-10 PIDs.
var userProcessTicksFixture = "stat " + processStatFixture(101, "com.example.app", 98765) + "\n" +
	"stat " + processStatFixture(202, "com.example.app:remote", 98800) + "\n"

// standardPackageDump is the installed identity the process transcripts report:
// application id 10140, installed for user 0.
var standardPackageDump = dumpsysPackageFixture("com.example.app", 10140, "fc909ae5", nil)
