package android

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
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

// fakeTranscripts is every replay the tests can select.
var fakeTranscripts = []string{
	"argv-record", "exit3", "remote-hash", "screenshot", "input",
	"hang", "probe", "capture",
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
			strings.HasPrefix(entry, "MOBILE_USE_FAKE_ADB_ARGV=") {
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
	case "exec-out":
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
	captureXML = `<hierarchy rotation="0">` +
		`<node class="android.widget.FrameLayout" package="com.example.app" display-id="0" visible-to-user="true" bounds="[0,0][1080,2400]">` +
		`<node class="android.widget.EditText" package="com.example.app" text="Query" resource-id="com.example.app:id/q" focused="true" clickable="true" enabled="true" visible-to-user="true" bounds="[0,100][1080,220]" />` +
		`</node>` +
		`<node class="android.widget.TextView" package="com.example.app" text="Other display" display-id="61" visible-to-user="true" bounds="[0,0][1080,2400]" />` +
		`</hierarchy>`
)
